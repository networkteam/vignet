package vignet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/apex/log"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gitHttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/networkteam/apexlogutils/httplog"

	"github.com/networkteam/vignet/yaml"
)

type Handler struct {
	mux http.Handler

	authorizer Authorizer
	config     Config
}

var _ http.Handler = &Handler{}

func NewHandler(
	authenticationProvider AuthenticationProvider,
	authorizer Authorizer,
	config Config,
) *Handler {
	h := &Handler{
		authorizer: authorizer,
		config:     config,
	}

	r := chi.NewRouter()

	r.Use(
		httpLogger,
	)

	r.Group(func(r chi.Router) {
		r.Use(AuthenticateRequest(authenticationProvider))

		r.Post("/patch/{repo}", h.patch)
	})

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h.mux = r

	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

type patchRequest struct {
	Commit struct {
		Message   string        `json:"message"`
		Committer *objSignature `json:"committer"`
		Author    *objSignature `json:"author"`
	} `json:"commit"`
	Commands []patchRequestCommand `json:"commands"`
}

type objSignature struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type patchRequestCommand struct {
	// Path to file to patch (relative to repository root)
	Path string `json:"path"`
	// SetField options are given, if the command should set the value of a (nested) field
	SetField *setFieldPatchRequestCommand `json:"setField"`
}

type setFieldPatchRequestCommand struct {
	// Field path to set (dot separated)
	Field string `json:"field"`
	// Value to set
	Value any `json:"value"`
	// CreateKeys will create missing keys for field if they don't exist, if set to true
	CreateKeys bool `json:"createKeys"`
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) {
	// Decode patch request from body
	var req patchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.WithError(err).Warn("Invalid JSON in request body")
		http.Error(w, fmt.Sprintf("Invalid JSON in body: %v", err), http.StatusBadRequest)
		return
	}

	// TODO Validate patchRequest (e.g. non-empty commands)

	ctx := r.Context()
	authCtx := authCtxFromCtx(ctx)

	log.
		WithField("gitLabClaims", authCtx.GitLabClaims).
		Debug("Authorizing request")

	repoName := chi.URLParam(r, "repo")
	var repoConfig RepositoryConfig
	if c, exists := h.config.Repositories[repoName]; !exists {
		log.WithField("repo", repoName).Warn("Unknown repository")
		http.Error(w, fmt.Sprintf("Unknown repository: %q", repoName), http.StatusNotFound)
		return
	} else {
		repoConfig = c
	}

	if err := h.authorizer.AllowPatch(ctx, authCtx, repoName, req); err != nil {
		if v, ok := err.(ViolationsResolver); ok {
			var msg strings.Builder
			for _, violation := range v.Violations() {
				msg.WriteString("- ")
				msg.WriteString(violation)
				msg.WriteString("\n")
			}

			log.
				WithField("repo", repoName).
				WithError(err).
				Warn("Failed to authorize patch request")
			http.Error(w, fmt.Sprintf("Authorization failed:\n\n%s", msg.String()), http.StatusForbidden)
			return
		}

		log.
			WithField("repo", repoName).
			WithError(err).
			Error("Unexpected error authorizing patch request")
		http.Error(w, "Authorization error", http.StatusInternalServerError)
		return
	}

	log.
		WithField("authCtx", authCtx.GitLabClaims).
		Debugf("Will patch %s with %+v", repoName, req)

	// TODO Extract handling of command to separate type
	err := h.gitClonePatchCommitPush(ctx, repoName, repoConfig, req)
	if err != nil {
		log.
			WithField("repo", repoName).
			WithError(err).
			Error("Failed to apply patch command to repository")
		http.Error(w, fmt.Sprintf("Patch command failed:\n\n%v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) gitClonePatchCommitPush(ctx context.Context, repoName string, repoConfig RepositoryConfig, req patchRequest) error {
	storer := memory.NewStorage()
	fs := memfs.New()

	var authMethod transport.AuthMethod
	if repoConfig.BasicAuth != nil {
		authMethod = &gitHttp.BasicAuth{
			Username: repoConfig.BasicAuth.Username,
			Password: repoConfig.BasicAuth.Password,
		}
	}
	r, err := git.Clone(storer, fs, &git.CloneOptions{
		URL:  repoConfig.URL,
		Auth: authMethod,
	})
	if err != nil {
		return fmt.Errorf("cloning repository: %w", err)
	}
	log.
		WithField("repoName", repoName).
		WithField("repoUrl", repoConfig.URL).
		Info("Cloned repository")

	w, err := r.Worktree()
	if err != nil {
		return fmt.Errorf("getting worktree for repository: %w", err)
	}

	for _, cmd := range req.Commands {
		// TODO Validate command (e.g. non-empty path)

		err := h.applyPatchCommand(ctx, fs, cmd)
		if err != nil {
			return fmt.Errorf("applying patch command to %q: %w", cmd.Path, err)
		}

		_, err = w.Add(cmd.Path)
		if err != nil {
			return fmt.Errorf("adding file to worktree: %w", err)
		}
	}

	commitMessage, commitOptions := h.buildCommitMsgAndOptions(ctx, req)
	commitHash, err := w.Commit(commitMessage, commitOptions)
	if err != nil {
		return fmt.Errorf("creating commit: %w", err)
	}

	err = r.Push(&git.PushOptions{
		RemoteName: "origin",
		Auth:       authMethod,
	})
	if err != nil {
		return fmt.Errorf("pushing to repository: %w", err)
	}

	log.
		WithField("repoName", repoName).
		WithField("repoUrl", repoConfig.URL).
		WithField("commitHash", commitHash).
		Info("Pushed commit to repository")

	return nil
}

func (h *Handler) buildCommitMsgAndOptions(ctx context.Context, req patchRequest) (string, *git.CommitOptions) {
	commitMessage := h.config.Commit.DefaultMessage
	if req.Commit.Message != "" {
		commitMessage = req.Commit.Message
	}
	var (
		commitAuthor    *object.Signature
		commitCommitter *object.Signature
	)
	if req.Commit.Author != nil {
		commitAuthor = &object.Signature{
			Name:  req.Commit.Author.Name,
			Email: req.Commit.Author.Email,
			When:  time.Now(),
		}
	} else {
		commitAuthor = &object.Signature{
			Name:  h.config.Commit.DefaultAuthor.Name,
			Email: h.config.Commit.DefaultAuthor.Email,
			When:  time.Now(),
		}
	}
	if req.Commit.Committer != nil {
		commitCommitter = &object.Signature{
			Name:  req.Commit.Committer.Name,
			Email: req.Commit.Committer.Email,
			When:  time.Now(),
		}
	} else {
		authCtx := authCtxFromCtx(ctx)
		if authCtx.GitLabClaims != nil {
			commitCommitter = &object.Signature{
				Name:  authCtx.GitLabClaims.UserLogin,
				Email: authCtx.GitLabClaims.UserEmail,
				When:  time.Now(),
			}
		}
	}

	commitOptions := &git.CommitOptions{
		Author:    commitAuthor,
		Committer: commitCommitter,
	}
	return commitMessage, commitOptions
}

func (h *Handler) applyPatchCommand(ctx context.Context, fs billy.Filesystem, cmd patchRequestCommand) error {
	f, err := fs.OpenFile(cmd.Path, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("opening file for reading: %w", err)
	}
	defer f.Close()

	// If file is not a YAML file, we return an error (for now)
	if !strings.HasSuffix(cmd.Path, ".yaml") && !strings.HasSuffix(cmd.Path, ".yml") {
		return fmt.Errorf("unsupported file type: %q, only YAML is supported for now", cmd.Path)
	}

	patcher, err := yaml.NewPatcher(f)
	if err != nil {
		return fmt.Errorf("opening YAML for patching: %w", err)
	}

	switch {
	case cmd.SetField != nil:
		err = patcher.SetField(strings.Split(cmd.SetField.Field, "."), cmd.SetField.Value, cmd.SetField.CreateKeys)
		if err != nil {
			return fmt.Errorf("setting field %q: %w", cmd.SetField.Field, err)
		}
	default:
		return fmt.Errorf("unknown command type")
	}

	err = f.Truncate(0)
	if err != nil {
		return fmt.Errorf("truncating file: %w", err)
	}

	_, err = f.Seek(0, io.SeekStart)
	if err != nil {
		return fmt.Errorf("seeking to start of file: %w", err)
	}

	err = patcher.Encode(f)
	if err != nil {
		return fmt.Errorf("encoding YAML: %w", err)
	}

	log.
		WithField("path", cmd.Path).
		Info("Patched YAML")

	return nil
}

func httpLogger(h http.Handler) http.Handler {
	return httplog.New(h, httplog.ExcludePathPrefix("/healthz"))
}

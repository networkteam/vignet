package vignet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
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

	"github.com/networkteam/vignet/httputil"
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
	Commit   patchRequestCommit    `json:"commit"`
	Commands []patchRequestCommand `json:"commands"`
}

type patchRequestCommit struct {
	Message   string        `json:"message"`
	Committer *objSignature `json:"committer"`
	Author    *objSignature `json:"author"`
}

func (c patchRequestCommit) Validate() error {
	if c.Committer != nil {
		if err := c.Committer.Validate(); err != nil {
			return fmt.Errorf("invalid 'committer': %w", err)
		}
	}
	if c.Author != nil {
		if err := c.Author.Validate(); err != nil {
			return fmt.Errorf("invalid 'author': %w", err)
		}
	}
	return nil
}

func (r patchRequest) Validate() error {
	if err := r.Commit.Validate(); err != nil {
		return fmt.Errorf("invalid 'commit': %w", err)
	}
	if len(r.Commands) == 0 {
		return fmt.Errorf("no 'commands' given")
	}
	for idx, cmd := range r.Commands {
		if err := cmd.Validate(); err != nil {
			return fmt.Errorf("'commands[%d]' is invalid: %w", idx, err)
		}
	}
	return nil
}

type objSignature struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (s objSignature) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("'name' must not be empty")
	}
	if s.Email == "" {
		return fmt.Errorf("'email' must not be empty")
	}
	return nil
}

type patchRequestCommand struct {
	// Path to file to patch (relative to repository root)
	Path string `json:"path"`
	// SetField options are given, if the command should set the value of a (nested) field
	SetField *setFieldPatchRequestCommand `json:"setField"`
	// CreateFile options are given, if the command should create a file
	CreateFile *createFilePatchRequestCommand `json:"createFile"`
}

func (c patchRequestCommand) Validate() error {
	if c.Path == "" {
		return fmt.Errorf("'path' must be set")
	}

	var commandsSet []string
	if c.SetField != nil {
		commandsSet = append(commandsSet, "'setField'")
	}
	if c.CreateFile != nil {
		commandsSet = append(commandsSet, "'createFile'")
	}
	if len(commandsSet) == 0 {
		return errors.New("no command is set")
	}
	if len(commandsSet) > 1 {
		return fmt.Errorf("only one command can be set, but %s are specified", strings.Join(commandsSet, ", "))
	}

	if c.SetField != nil {
		if err := c.SetField.Validate(); err != nil {
			return fmt.Errorf("invalid 'setField' command: %w", err)
		}
	}
	if c.CreateFile != nil {
		if err := c.CreateFile.Validate(); err != nil {
			return fmt.Errorf("invalid 'createFile' command: %w", err)
		}
	}

	return nil
}

type setFieldPatchRequestCommand struct {
	// Field path to set (dot separated)
	Field string `json:"field"`
	// Value to set
	Value any `json:"value"`
	// Create missing keys for field if they don't exist, if set to true
	Create bool `json:"create"`
}

var yamlPathPattern = regexp.MustCompile(`^([\w-]+\.)*[\w-]+$`)

func (c setFieldPatchRequestCommand) Validate() error {
	if c.Field == "" {
		return fmt.Errorf("field must not be empty")
	}
	// Validate Field is a valid path of YAML keys
	if !yamlPathPattern.MatchString(c.Field) {
		return fmt.Errorf("field must be a valid path of dot separated YAML keys")
	}

	return nil
}

type createFilePatchRequestCommand struct {
	// Content of the file to set
	Content string `json:"content"`
}

func (c createFilePatchRequestCommand) Validate() error {
	return nil
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) {
	// Decode patch request from body
	var req patchRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		log.WithError(err).Warn("Invalid JSON in request body")
		respondError(w, r, "Invalid JSON in body", clientError{err, http.StatusBadRequest})
		return
	}

	err := req.Validate()
	if err != nil {
		log.WithField("patchRequest", req).WithError(err).Warn("Invalid patch request")
		respondError(w, r, "Validation of request failed", clientError{err, http.StatusBadRequest})
		return
	}

	ctx := r.Context()
	authCtx := authCtxFromCtx(ctx)

	log.
		WithField("gitLabClaims", authCtx.GitLabClaims).
		Debug("Authorizing request")

	repoName := chi.URLParam(r, "repo")
	var repoConfig RepositoryConfig
	if c, exists := h.config.Repositories[repoName]; !exists {
		log.WithField("repo", repoName).Warn("Unknown repository")
		respondError(w, r, "Unknown repository", clientError{fmt.Errorf("repository %q not configured", repoName), http.StatusNotFound})
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
			respondError(w, r, "Authorization failed", clientError{errors.New(msg.String()), http.StatusForbidden})
			return
		}

		log.
			WithField("repo", repoName).
			WithError(err).
			Error("Unexpected error authorizing patch request")
		respondError(w, r, "Authorization error", nil)
		return
	}

	log.
		WithField("authCtx", authCtx.GitLabClaims).
		Debugf("Will patch %s with %+v", repoName, req)

	// TODO Extract handling of command to separate type
	err = h.gitClonePatchCommitPush(ctx, repoName, repoConfig, req)
	if err != nil {
		var clientErr clientError
		if errors.As(err, &clientErr) {
			log.
				WithField("repo", repoName).
				WithError(err).
				Warn("Failed to apply patch command to repository")
		} else {
			log.
				WithField("repo", repoName).
				WithError(err).
				Error("Failed to apply patch command to repository")
		}
		respondError(w, r, "Patch failed", err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

type errorResponse struct {
	Cause string `json:"cause"`
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`
}

func respondError(w http.ResponseWriter, r *http.Request, cause string, err error) {
	var clientErr clientError
	statusCode := http.StatusInternalServerError
	errorMsg := "" // Only output detailed error message if we have a client error (which should be safe to expose)
	if errors.As(err, &clientErr) {
		statusCode = clientErr.status
		if clientErr.error != nil {
			errorMsg = clientErr.error.Error()
		}
	}

	var code string
	var codedError codedError
	if errors.As(err, &codedError) {
		code = codedError.code
	}

	// Negotiate response format
	contentType := httputil.NegotiateContentType(r, []string{"text/plain", "application/json"}, "text/plain")
	switch contentType {
	case "application/json":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(errorResponse{
			Cause: cause,
			Error: errorMsg,
			Code:  code,
		})
	default:
		if code != "" {
			w.Header().Set("X-Error-Code", code)
		}
		if errorMsg != "" {
			http.Error(w, fmt.Sprintf("%s:\n\n%v", cause, errorMsg), statusCode)
		} else {
			http.Error(w, cause, statusCode)
		}
	}
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

type clientError struct {
	error  error
	status int
}

func (e clientError) Error() string {
	if e.error == nil {
		return ""
	}
	return e.error.Error()
}

func (e clientError) Unwrap() error {
	return e.error
}

type codedError struct {
	error error
	code  string
}

func (e codedError) Error() string {
	if e.error == nil {
		return e.code
	}
	return fmt.Sprintf("%s (%s)", e.error.Error(), e.code)
}

func (e codedError) Unwrap() error {
	return e.error
}

func (h *Handler) applyPatchCommand(ctx context.Context, fs billy.Filesystem, cmd patchRequestCommand) error {
	// If file is not a YAML file, we return an error (for now)
	if !strings.HasSuffix(cmd.Path, ".yaml") && !strings.HasSuffix(cmd.Path, ".yml") {
		return clientError{fmt.Errorf("unsupported file type: %q, only YAML is supported for now", cmd.Path), http.StatusUnprocessableEntity}
	}

	switch {
	case cmd.CreateFile != nil:
		f, err := fs.Create(cmd.Path)
		if err != nil {
			return fmt.Errorf("creating file: %w", err)
		}
		defer f.Close()

		_, err = f.Write([]byte(cmd.CreateFile.Content))
		if err != nil {
			return fmt.Errorf("writing content: %w", err)
		}
	case cmd.SetField != nil:
		f, err := fs.OpenFile(cmd.Path, os.O_RDWR, 0644)
		if err != nil {
			if os.IsNotExist(err) {
				return clientError{fmt.Errorf("file %s does not exist", cmd.Path), http.StatusUnprocessableEntity}
			}
			return fmt.Errorf("opening file read-write: %w", err)
		}
		defer f.Close()

		patcher, err := yaml.NewPatcher(f)
		if err != nil {
			return fmt.Errorf("reading YAML: %w", err)
		}

		err = patcher.SetField(strings.Split(cmd.SetField.Field, "."), cmd.SetField.Value, cmd.SetField.Create)
		if err != nil {
			return clientError{fmt.Errorf("setting field %q: %w", cmd.SetField.Field, err), http.StatusUnprocessableEntity}
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
			return fmt.Errorf("writing YAML: %w", err)
		}
	default:
		return clientError{fmt.Errorf("unknown command type"), http.StatusBadRequest}
	}

	log.
		WithField("path", cmd.Path).
		Info("Patched YAML")

	return nil
}

func httpLogger(h http.Handler) http.Handler {
	return httplog.New(h, httplog.ExcludePathPrefix("/healthz"))
}

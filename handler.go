package vignet

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/apex/log"
	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gitHttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

type Handler struct {
	mux http.Handler

	authorizer         Authorizer
	repositoriesConfig RepositoriesConfig
}

var _ http.Handler = &Handler{}

func NewHandler(
	authenticationProvider AuthenticationProvider,
	authorizer Authorizer,
	repositoriesConfig RepositoriesConfig,
) *Handler {
	h := &Handler{
		authorizer:         authorizer,
		repositoriesConfig: repositoriesConfig,
	}

	r := chi.NewRouter()
	r.Use(AuthenticateRequest(authenticationProvider))

	r.Post("/patch/{repo}", h.patch)

	h.mux = r

	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

type patchRequest struct {
	Commands []patchRequestCommand `json:"commands"`
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
	// Value to set (as YAML string)
	Value string `json:"value"`
}

func (h *Handler) patch(w http.ResponseWriter, r *http.Request) {
	// Decode patch request from body
	var req patchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.WithError(err).Warn("Invalid JSON in request body")
		http.Error(w, fmt.Sprintf("Invalid JSON in body: %v", err), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	authCtx := authCtxFromCtx(ctx)

	log.
		WithField("gitLabClaims", authCtx.GitLabClaims).
		Debug("Authorizing request")

	repoName := chi.URLParam(r, "repo")
	var repoConfig RepositoryConfig
	if c, exists := h.repositoriesConfig[repoName]; !exists {
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

	err := h.gitClonePatchCommitPush(ctx, repoName, repoConfig, req)
	if err != nil {
		log.
			WithError(err).
			Error("Failed to apply patch command to repository")
		http.Error(w, "Patch command failed", http.StatusInternalServerError)
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
		// FIXME Git mock server doesn't support shallow clone (`unsupported capability: shallow`)
		// Depth: 1,
	})
	if err != nil {
		return fmt.Errorf("cloning repository: %w", err)
	}
	log.
		WithField("repoName", repoName).
		WithField("repoUrl", repoConfig.URL).
		Info("Cloned repository")

	w, err := r.Worktree()
	_ = w
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

	// TODO Get commit message from patch request
	_, err = w.Commit("Performed patch", &git.CommitOptions{
		// TODO Set author when running headless
		// Author: ,
	})
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
		Info("Pushed commit to repository")

	return nil
}

func (h *Handler) applyPatchCommand(ctx context.Context, fs billy.Filesystem, cmd patchRequestCommand) error {
	var in []byte

	// Read file content from path (in closure to use proper defer)
	err := (func() error {
		f, err := fs.Open(cmd.Path)
		if err != nil {
			return fmt.Errorf("opening file for reading: %w", err)
		}
		defer f.Close()

		in, err = ioutil.ReadAll(f)
		if err != nil {
			return fmt.Errorf("reading file: %w", err)
		}
		return nil
	})()
	if err != nil {
		return err
	}

	res, err := yaml.Parse(string(in))
	if err != nil {
		return fmt.Errorf("parsing YAML: %w", err)
	}

	switch {
	case cmd.SetField != nil:
		/*
			FIXME This works only if the image tag is already present!
			err = res.PipeE(
				yaml.Lookup("spec", "values", "image", "tag"),
				yaml.Set(yaml.NewStringRNode("1.2.3")),
			)
		*/
		err = res.SetMapField(yaml.NewStringRNode(cmd.SetField.Value), strings.Split(cmd.SetField.Field, ".")...)
		if err != nil {
			return fmt.Errorf("setting field: %w", err)
		}
	default:
		return fmt.Errorf("unknown command type")
	}

	err = (func() error {
		f, err := fs.Create(cmd.Path)
		if err != nil {
			return fmt.Errorf("opening file for writing: %w", err)
		}
		defer f.Close()

		out, err := res.String()
		if err != nil {
			return fmt.Errorf("serializing YAML: %w", err)
		}

		_, err = f.Write([]byte(out))
		if err != nil {
			return fmt.Errorf("writing file: %w", err)
		}
		return nil
	})()
	if err != nil {
		return err
	}

	log.
		WithField("path", cmd.Path).
		Info("Patched YAML")

	return nil
}

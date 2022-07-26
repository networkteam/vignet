package vignet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/apex/log"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	mux http.Handler

	authorizer Authorizer
}

var _ http.Handler = &Handler{}

func NewHandler(
	authenticationProvider AuthenticationProvider,
	authorizer Authorizer,
) *Handler {
	h := &Handler{
		authorizer: authorizer,
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

	repo := chi.URLParam(r, "repo")
	// TODO Check if repo is actually configured!

	if err := h.authorizer.AllowPatch(ctx, authCtx, repo, req); err != nil {
		if v, ok := err.(ViolationsResolver); ok {
			var msg strings.Builder
			for _, violation := range v.Violations() {
				msg.WriteString("- ")
				msg.WriteString(violation)
				msg.WriteString("\n")
			}

			log.
				WithField("repo", repo).
				WithError(err).
				Warn("Failed to authorize patch request")
			http.Error(w, fmt.Sprintf("Authorization failed:\n\n%s", msg.String()), http.StatusForbidden)
			return
		}

		log.
			WithField("repo", repo).
			WithError(err).
			Error("Unexpected error authorizing patch request")
		http.Error(w, "Authorization error", http.StatusInternalServerError)
		return
	}

	log.
		WithField("authCtx", authCtx.GitLabClaims).
		Debugf("Will patch %s with %+v", repo, req)

	// TODO Actually clone the repo, apply commands and push the changes
}

package vignet

import (
	"net/http"

	"github.com/apex/log"
	"github.com/golang-jwt/jwt/v4"
)

type GitLabClaims struct {
	jwt.RegisteredClaims

	NamespaceID    string `json:"namespace_id"`
	NamespacePath  string `json:"namespace_path"`
	ProjectID      string `json:"project_id"`
	ProjectPath    string `json:"project_path"`
	UserID         string `json:"user_id"`
	UserLogin      string `json:"user_login"`
	UserEmail      string `json:"user_email"`
	PipelineID     string `json:"pipeline_id"`
	PipelineSource string `json:"pipeline_source"`
	JobID          string `json:"job_id"`
	Ref            string `json:"ref"`
	RefType        string `json:"ref_type"`
	RefProtected   string `json:"ref_protected"`
}

type AuthCtx struct {
	// Error is set if the authentication failed.
	Error error `json:"error"`
	// GitLabClaims is set for GitLab authentication provider if no authenticated error occurred.
	GitLabClaims *GitLabClaims `json:"gitLabClaims"`
}

type AuthenticationProvider interface {
	// AuthCtxFromRequest builds an authentication context from the given requests.
	//
	// If a client error concerning the authentication is encountered or the request could not be authenticated, the error is set in AuthCtx.
	// If an internal error is encountered, the error is returned as error return value.
	AuthCtxFromRequest(r *http.Request) (AuthCtx, error)
}

// AuthenticateRequest is a middleware to set the AuthCtx from the given request on the request context.
func AuthenticateRequest(authenticationProvider AuthenticationProvider) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			authCtx, err := authenticationProvider.AuthCtxFromRequest(r)
			if err != nil {
				log.WithError(err).Errorf("An internal error occurred while authenticating request with %T", authenticationProvider)
				http.Error(w, "Authentication failed", http.StatusInternalServerError)
				return
			}
			if authCtx.Error != nil {
				log.WithError(authCtx.Error).Warnf("Authentication failed for request with %T", authenticationProvider)
				http.Error(w, "Authentication failed", http.StatusUnauthorized)
				return
			}
			ctx = ctxWithAuthCtx(ctx, authCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

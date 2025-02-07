package vignet

import (
  "context"
  "fmt"
  "net/http"
  netUrl "net/url"
  "strings"

  "github.com/MicahParks/keyfunc"
  "github.com/golang-jwt/jwt/v4"
)

type GitLabAuthenticationProvider struct {
  jwks *keyfunc.JWKS
}

var _ AuthenticationProvider = &GitLabAuthenticationProvider{}

// NewGitLabAuthenticationProvider creates a new GitLabAuthenticationProvider.
//
// It takes the GitLab instance URL as an argument.
// The context is used to cancel the refreshing of keys.
func NewGitLabAuthenticationProvider(ctx context.Context, url string) (*GitLabAuthenticationProvider, error) {
  parsedURL, err := netUrl.Parse(url)
  if err != nil {
    return nil, fmt.Errorf("invalid URL: %w", err)
  }

  parsedURL.Path = "/oauth/discovery/keys"

  jwks, err := keyfunc.Get(parsedURL.String(), keyfunc.Options{
    Ctx: ctx,
  })
  if err != nil {
    return nil, fmt.Errorf("loading JWKS: %w", err)
  }

  p := &GitLabAuthenticationProvider{
    jwks: jwks,
  }

  return p, nil
}

func (p *GitLabAuthenticationProvider) AuthCtxFromRequest(r *http.Request) (AuthCtx, error) {
  authorizationHeader := r.Header.Get("Authorization")
  if authorizationHeader == "" {
    return AuthCtx{
      Error: fmt.Errorf("missing Authorization header"),
    }, nil
  }
  const bearerPrefix = "Bearer "
  if !strings.HasPrefix(authorizationHeader, bearerPrefix) {
    return AuthCtx{
      Error: fmt.Errorf("invalid Bearer scheme in Authorization header"),
    }, nil
  }
  encodedJWT := authorizationHeader[len(bearerPrefix):]

  token, err := jwt.ParseWithClaims(encodedJWT, &GitLabClaims{}, p.jwks.Keyfunc, jwt.WithValidMethods([]string{"RS256"}))
  if err != nil {
    return AuthCtx{
      Error: fmt.Errorf("parsing JWT: %w", err),
    }, nil
  }

  claims := token.Claims.(*GitLabClaims)
  return AuthCtx{
    GitLabClaims: claims,
  }, nil
}

package vignet_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/require"

	"github.com/networkteam/vignet"
)

func Test_GitLabAuthenticationProvider_AuthCtxFromRequest(t *testing.T) {
	// Start mock server for JWKs

	// Generate RSA key
	ks := generateJwkSet(t)

	// Start mock server to serve JWKs
	jwksSrv := httptest.NewServer(jwksHandler(t, ks))
	defer jwksSrv.Close()

	serialized := buildJWT(t, ks)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	authProvider, err := vignet.NewGitLabAuthenticationProvider(ctx, jwksSrv.URL)
	require.NoError(t, err)

	req, _ := http.NewRequest("POST", "/foo", nil)
	req.Header.Set("Authorization", "Bearer "+string(serialized))
	authCtx, err := authProvider.AuthCtxFromRequest(req)
	require.NoError(t, err)

	require.NotNil(t, authCtx.GitLabClaims)
	require.Equal(t, "my-group/my-project", authCtx.GitLabClaims.ProjectPath)
}

func buildJWT(t *testing.T, ks jwk.Set) []byte {
	tok, err := jwt.
		NewBuilder().
		Issuer("test").
		Claim("project_path", "my-group/my-project").
		Build()
	require.NoError(t, err)

	key, _ := ks.Key(0)
	serialized, err := jwt.
		NewSerializer().
		Sign(jwt.WithKey(jwa.RS256, key)).
		Serialize(tok)
	require.NoError(t, err)

	return serialized
}

func jwksHandler(t *testing.T, ks jwk.Set) http.Handler {
	pubks, err := jwk.PublicSetOf(ks)
	if err != nil {
		panic(err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Log("responding to JWKs request")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		_ = json.NewEncoder(w).Encode(pubks)
	})
}

func generateJwkSet(t *testing.T) jwk.Set {
	t.Helper()

	v, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(t, err)

	// Generate fingerprint of public key
	v.Public()

	key, err := jwk.FromRaw(v)
	require.NoError(t, err)

	key.Set(jwk.AlgorithmKey, "RS256")
	key.Set(jwk.KeyUsageKey, "sig")
	kid := uuid.Must(uuid.NewV4())
	err = key.Set(jwk.KeyIDKey, kid.String())
	require.NoError(t, err)

	ks := jwk.NewSet()
	_ = ks.AddKey(key)

	return ks
}

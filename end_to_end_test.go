package vignet_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/object"
	gitHttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/stretchr/testify/require"

	"github.com/networkteam/vignet"
	"github.com/networkteam/vignet/policy"
)

func TestEndToEnd(t *testing.T) {
	tt := []struct {
		name               string
		patchPayload       string
		expectedError      string
		expectedStatus     int
		expectedGitContent map[string]fileExpectation
		multipartFiles     map[string]string
	}{
		{
			name: "valid setField with new key and create",
			patchPayload: `
				{
				  "commands": [
					{
					  "path": "my-group/my-project/release.yml",
					  "setField": {
						"field": "spec.values.image.tag",
						"value": "1.2.3",
						"create": true
					  }
					}
				  ]
				}
			`,
			expectedGitContent: map[string]fileExpectation{
				"my-group/my-project/release.yml": content{`foo: bar
spec:
  values:
    image:
      tag: 1.2.3
`},
			},
		},
		{
			name: "valid multiple setField with JSONPath",
			patchPayload: `
				{
				  "commands": [
					{
					  "path": "my-group/my-project/deployment.yml",
					  "setField": {
						"field": "spec.template.spec.containers[0].image",
						"value": "test.example.com:0.2.0"
					  }
					},
					{
					  "path": "my-group/my-project/deployment.yml",
					  "setField": {
						"field": "spec.template.spec.containers[0].env[?(@.name == 'BUILD_ID')].value",
						"value": "42"
					  }
					}
				  ]
				}
			`,
			expectedGitContent: map[string]fileExpectation{
				"my-group/my-project/deployment.yml": content{`spec:
  template:
    spec:
      containers:
        - name: test
          image: test.example.com:0.2.0
          env:
            - name: BUILD_ID
              value: '42'
`},
			},
		},
		{
			name: "invalid setField with new key and no create",
			patchPayload: `
				{
				  "commands": [
					{
					  "path": "my-group/my-project/release.yml",
					  "setField": {
						"field": "spec.values.image.tag",
						"value": "1.2.3"
					  }
					}
				  ]
				}
			`,
			expectedStatus: 422,
			expectedError:  `no nodes matched path`,
		},
		{
			name: "invalid setField with non-existing file",
			patchPayload: `
				{
				  "commands": [
					{
					  "path": "my-group/my-project/unknown.yml",
					  "setField": {
						"field": "spec.values.image.tag",
						"value": "1.2.3"
					  }
					}
				  ]
				}
			`,
			expectedStatus: 422,
			expectedError:  `file does not exist`,
		},
		{
			name: "valid create",
			patchPayload: `
				{
				  "commands": [
					{
					  "path": "my-group/my-project/new.yml",
					  "createFile": {
						"content": "---\nfoo: bar #Test\n"
					  }
					}
				  ]
				}
			`,
			expectedGitContent: map[string]fileExpectation{
				"my-group/my-project/release.yml": content{`foo: bar`},
				"my-group/my-project/new.yml":     content{"---\nfoo: bar #Test\n"},
			},
		},
		{
			name: "invalid create with existing file",
			patchPayload: `
				{
				  "commands": [
					{
					  "path": "my-group/my-project/release.yml",
					  "createFile": {
						"content": "---\nfoo: bar #Test\n"
					  }
					}
				  ]
				}
			`,
			expectedStatus: 422,
			expectedError:  "file already exists",
		},
		{
			name: "valid delete",
			patchPayload: `
				{
				  "commands": [
					{
					  "path": "my-group/my-project/release.yml",
					  "deleteFile": {}
					}
				  ]
				}
			`,
			expectedGitContent: map[string]fileExpectation{
				"my-group/my-project/release.yml": deleted{},
			},
		},
		{
			name: "invalid delete with non-existing file",
			patchPayload: `
				{
				  "commands": [
					{
					  "path": "my-group/my-project/unknown.yml",
					  "deleteFile": {}
					}
				  ]
				}
			`,
			expectedStatus: 422,
			expectedError:  "file does not exist",
		},
	}

	// - Generate JWK key set
	ks := generateJwkSet(t)

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			// --- Start mock server for JWKs
			// - Start mock server to serve JWKs for authorizer
			jwksSrv := httptest.NewServer(jwksHandler(t, ks))
			defer jwksSrv.Close()

			// --- Start mock Git HTTP server
			// - Initialize Git repository with some content
			fs := memfs.New()
			initGitRepo(t, fs, map[string]string{
				"my-group/my-project/release.yml": "foo: bar",
				"other/file.yml":                  "version: 123",
				"my-group/my-project/deployment.yml": `spec:
  template:
    spec:
      containers:
        - name: test
          image: test.example.com:0.1.0
          env:
            - name: BUILD_ID
              value: '1'
`,
			})
			// - Start mock HTTP Git server with basic auth
			gitSrv := httptest.NewServer(newMockHttpGitServer(fs, mockHttpGitServerOpts{basicAuth: &gitHttp.BasicAuth{
				Username: "j.doe",
				Password: "not-a-secret",
			}}))
			defer gitSrv.Close()

			// --- Setup HTTP handler
			// - Initialize GitLab authentication provider using the JWKs server
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			authProvider, err := vignet.NewGitLabAuthenticationProvider(ctx, jwksSrv.URL)
			require.NoError(t, err)

			// - Initialize authorizer with default policy
			defaultBundle, err := policy.LoadDefaultBundle()
			require.NoError(t, err)
			authorizer, err := vignet.NewRegoAuthorizer(ctx, defaultBundle)
			require.NoError(t, err)

			// - Create handler
			handler := vignet.NewHandler(authProvider, authorizer, vignet.Config{
				Repositories: vignet.RepositoriesConfig{
					"e2e-test": {
						URL: gitSrv.URL,
						BasicAuth: &vignet.BasicAuthConfig{
							Username: "j.doe",
							Password: "not-a-secret",
						},
					},
				},
				Commit: vignet.CommitConfig{
					DefaultMessage: "Bumped release",
				},
			})

			// --- Build patch request
			// - Build a simulated JWT coming from GitLab Job (CI_JOB_JWT)
			serializedJWT := buildJWT(t, ks)
			req, _ := http.NewRequest("POST", "/patch/e2e-test", strings.NewReader(tc.patchPayload))
			req.Header.Set("Authorization", "Bearer "+string(serializedJWT))

			// --- Perform request
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			// --- Assert response
			if tc.expectedStatus == 0 {
				require.Equal(t, http.StatusOK, rec.Code)
			} else {
				require.Equal(t, tc.expectedStatus, rec.Code)
			}

			if tc.expectedError != "" {
				require.Contains(t, rec.Body.String(), tc.expectedError)
				return
			}

			// --- Assert Git repository contains change
			assertGitRepoHeadCommit(t, fs, "Bumped release")
			assertGitRepoContains(t, fs, tc.expectedGitContent)
		})
	}
}

// --- Helper types to have a nicer API to build the test cases

type content struct{ string }

func (c content) content() string { return c.string }
func (content) isDeleted() bool   { return false }

type deleted struct{}

func (deleted) content() string { return "" }
func (deleted) isDeleted() bool { return true }

type fileExpectation interface {
	content() string
	isDeleted() bool
}

func assertGitRepoHeadCommit(t *testing.T, fs billy.Filesystem, expectedMessage string) {
	t.Helper()

	storer := filesystem.NewStorage(fs, cache.NewObjectLRUDefault())
	defer storer.Close()

	repo, err := git.Open(storer, nil)
	require.NoError(t, err)

	head, err := repo.Head()
	require.NoError(t, err)

	commit, err := repo.CommitObject(head.Hash())
	require.NoError(t, err)

	require.Equal(t, expectedMessage, commit.Message)
}

func assertGitRepoContains(t *testing.T, fs billy.Filesystem, expectedFiles map[string]fileExpectation) {
	t.Helper()

	storer := filesystem.NewStorage(fs, cache.NewObjectLRUDefault())
	defer storer.Close()
	workdirFS := memfs.New()
	repo, err := git.Open(storer, workdirFS)
	require.NoError(t, err)

	w, err := repo.Worktree()
	require.NoError(t, err)

	// The trick part: reset will apply the Git repo storage to the in-memory workdir filesystem
	err = w.Reset(&git.ResetOptions{
		Mode: git.HardReset,
	})
	require.NoError(t, err)

	// Check files
	for path, expectation := range expectedFiles {

		switch v := (expectation).(type) {
		case content:
			f, err := workdirFS.Open(path)
			require.NoError(t, err)
			b, _ := io.ReadAll(f)
			require.NoError(t, err)
			f.Close()

			// Assert content
			require.Equal(t, v.string, string(b))
		case deleted:
			_, err := workdirFS.Stat(path)
			require.ErrorIs(t, err, os.ErrNotExist)
		}

	}
}

func initGitRepo(t *testing.T, fs billy.Filesystem, initialFiles map[string]string) {
	t.Helper()

	storer := filesystem.NewStorage(fs, cache.NewObjectLRUDefault())
	defer storer.Close()

	workdirFS := memfs.New()
	repo, err := git.Init(storer, workdirFS)
	require.NoError(t, err)

	// Create initial files
	for path, content := range initialFiles {
		(func() {
			f, err := workdirFS.Create(path)
			require.NoError(t, err)
			defer f.Close()

			_, err = f.Write([]byte(content))
			require.NoError(t, err)
		})()
	}

	// Add files
	w, err := repo.Worktree()
	require.NoError(t, err)

	for path := range initialFiles {
		_, err := w.Add(path)
		require.NoError(t, err)
	}

	_, err = w.Commit("Initial commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "vignet",
			Email: "test@vignet",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)
}

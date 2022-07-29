package vignet

import (
	"context"
	"fmt"
)

type Config struct {
	// AuthenticationProvider configures the authentication provider to use for authenticating requests.
	AuthenticationProvider struct {
		Type AuthenticationProviderType `yaml:"type"`
		// GitLab must be set for type `gitlab`
		GitLab *struct {
			URL string `yaml:"url"`
		} `yaml:"gitlab"`
	} `yaml:"authenticationProvider"`

	// Repositories indexed by an identifier.
	Repositories RepositoriesConfig `yaml:"repositories"`

	// Commit configures commit options when creating a new commit.
	Commit CommitConfig `yaml:"commit"`
}

// DefaultConfig is the default configuration that will be overwritten by the configuration file.
var DefaultConfig = Config{
	Commit: CommitConfig{
		DefaultMessage: "Automated patch by vignet",
		DefaultAuthor: SignatureConfig{
			Name:  "vignet",
			Email: "bot@vignet",
		},
	},
}

func (c Config) Validate() error {
	if len(c.Repositories) == 0 {
		return fmt.Errorf("invalid repositories: empty")
	}
	if !c.AuthenticationProvider.Type.IsValid() {
		return fmt.Errorf("invalid authenticationProvider.type: %q", c.AuthenticationProvider.Type)
	}
	if err := c.Commit.DefaultAuthor.Valid(); err != nil {
		return fmt.Errorf("invalid commit.defaultAuthor: %w", err)
	}

	return nil
}

type RepositoriesConfig map[string]RepositoryConfig

type RepositoryConfig struct {
	URL       string           `yaml:"url"`
	BasicAuth *BasicAuthConfig `yaml:"basicAuth"`
}

type BasicAuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

type SignatureConfig struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

func (c SignatureConfig) Valid() error {
	if c.Name == "" {
		return fmt.Errorf("name required")
	}
	if c.Email == "" {
		return fmt.Errorf("email required")
	}
	return nil
}

type CommitConfig struct {
	DefaultMessage string          `yaml:"defaultMessage"`
	DefaultAuthor  SignatureConfig `yaml:"defaultAuthor"`
}

type AuthenticationProviderType string

const (
	AuthenticationProviderGitLab AuthenticationProviderType = "gitlab"
)

func (p AuthenticationProviderType) IsValid() bool {
	switch p {
	case AuthenticationProviderGitLab:
		return true
	default:
		return false
	}
}

func (c Config) BuildAuthenticationProvider(ctx context.Context) (AuthenticationProvider, error) {
	switch c.AuthenticationProvider.Type {
	case AuthenticationProviderGitLab:
		p, err := NewGitLabAuthenticationProvider(ctx, c.AuthenticationProvider.GitLab.URL)
		if err != nil {
			return nil, fmt.Errorf("initializing GitLab authentication provider: %w", err)
		}
		return p, nil
	default:
		return nil, fmt.Errorf("unsupported authentication provider: %q", c.AuthenticationProvider.Type)
	}
}

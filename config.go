package vignet

import (
	"context"
	"fmt"
)

type Config struct {
	AuthenticationProvider struct {
		Type   AuthenticationProviderType `yaml:"type"`
		GitLab struct {
			URL string `yaml:"url"`
		}
	} `yaml:"authenticationProvider"`
	// Repositories indexed by an identifier.
	Repositories RepositoriesConfig `yaml:"repositories"`
}

func (c Config) Validate() error {
	if len(c.Repositories) == 0 {
		return fmt.Errorf("no repositories configured")
	}
	if !c.AuthenticationProvider.Type.IsValid() {
		return fmt.Errorf("invalid authentication provider: %q", c.AuthenticationProvider)
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

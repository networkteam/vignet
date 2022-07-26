package policy

import (
	"embed"
	"fmt"

	"github.com/open-policy-agent/opa/bundle"
)

//go:embed *.rego
var DefaultPolicies embed.FS

func LoadDefaultBundle() (*bundle.Bundle, error) {
	fsLoader, err := bundle.NewFSLoader(DefaultPolicies)
	if err != nil {
		return nil, fmt.Errorf("creating loader: %w", err)
	}
	reader := bundle.NewCustomReader(fsLoader)

	b, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("reading bundle: %w", err)
	}

	return &b, nil
}

func LoadBundle(path string) (*bundle.Bundle, error) {
	dirLoader := bundle.NewDirectoryLoader(path)
	reader := bundle.NewCustomReader(dirLoader)

	b, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("reading bundle: %w", err)
	}

	return &b, nil
}

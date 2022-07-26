package vignet

import (
	"context"
	"fmt"
	"strings"

	"github.com/open-policy-agent/opa/bundle"
	"github.com/open-policy-agent/opa/rego"
)

type Authorizer interface {
	AllowPatch(ctx context.Context, authCtx AuthCtx, repo string, req patchRequest) error
}

type RegoAuthorizer struct {
	patchAllowQuery rego.PreparedEvalQuery
}

var _ Authorizer = &RegoAuthorizer{}

func NewRegoAuthorizer(ctx context.Context, bundle *bundle.Bundle) (*RegoAuthorizer, error) {
	patchAllowQuery, err := rego.New(
		rego.Query("data.vignet.request.patch.violations[msg]"),
		rego.ParsedBundle("default", bundle),
		// Set strict errors for built-in function errors (e.g. wrong operand types)
		rego.StrictBuiltinErrors(true),
	).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("preparing query: %w", err)
	}

	return &RegoAuthorizer{
		patchAllowQuery: patchAllowQuery,
	}, nil
}

type patchInput struct {
	Repo         string       `json:"repo"`
	PatchRequest patchRequest `json:"patchRequest"`
	AuthCtx      AuthCtx      `json:"authCtx"`
}

func (r *RegoAuthorizer) AllowPatch(ctx context.Context, authCtx AuthCtx, repo string, req patchRequest) error {
	input := patchInput{
		Repo:         repo,
		PatchRequest: req,
		AuthCtx:      authCtx,
	}

	results, err := r.patchAllowQuery.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return fmt.Errorf("evaluating query: %w", err)
	}
	// No result means no violations
	if len(results) == 0 {
		return nil
	}

	var violations []string

	for _, result := range results {
		if b, ok := result.Bindings["msg"]; !ok {
			return fmt.Errorf("expected binding \"msg\" for query result")
		} else {
			if msg, ok := b.(string); !ok {
				return fmt.Errorf("expected string for binding \"msg\"")
			} else {
				violations = append(violations, msg)
			}
		}
	}

	return authorizerViolationsError(violations)
}

type ViolationsResolver interface {
	Violations() []string
}

type authorizerViolationsError []string

func (v authorizerViolationsError) Error() string {
	if len(v) == 1 {
		return fmt.Sprintf("violation: %s", v[0])
	}

	return fmt.Sprintf("violations: %v", strings.Join(v, "; "))
}

func (v authorizerViolationsError) Violations() []string {
	return v
}

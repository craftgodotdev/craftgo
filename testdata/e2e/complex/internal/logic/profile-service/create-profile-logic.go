// Package profileservice carries the business logic for the complex
// fixture's ProfileService. Hand-written; survives `craftgo gen`.
package profileservice

import (
	"context"
	"fmt"
	"strings"

	types "github.com/dropship-dev/craftgo/testdata/e2e/complex/internal/types/design"
	"github.com/dropship-dev/craftgo/testdata/e2e/complex/svccontext"
)

// CreateProfileLogic carries per-request state for CreateProfile.
type CreateProfileLogic struct {
	ctx    context.Context
	svcCtx *svccontext.ServiceContext
}

// NewCreateProfileLogic returns a fresh logic instance.
func NewCreateProfileLogic(ctx context.Context, svcCtx *svccontext.ServiceContext) *CreateProfileLogic {
	return &CreateProfileLogic{ctx: ctx, svcCtx: svcCtx}
}

// CreateProfile applies the business rules schema validation can't reach,
// then stores the profile. Possible errors:
//   - DuplicateEmail (HTTP 409) when the email is already taken.
//   - ProfileValidationFailed (HTTP 422) when a business rule fails (e.g.,
//     reserved display names).
//   - RateLimited (HTTP 429) when the demo-quota for the source IP is hit.
func (l *CreateProfileLogic) CreateProfile(req *types.CreateProfileReq) (*types.Profile, error) {
	l.svcCtx.Lock()
	defer l.svcCtx.Unlock()

	// Reserved display names — schema validation cannot encode this so it
	// becomes a 422 with the offending paths listed. `Code` mirrors the
	// `@default(...)` value declared in the DSL so the wire envelope
	// always carries the canonical machine-readable code.
	if isReserved(req.DisplayName) {
		return nil, types.NewProfileValidationFailedErr(types.ProfileValidationFailedBody{
			Code:   "PROFILE_VALIDATION_FAILED",
			Fields: []string{"displayName"},
		})
	}

	// Email uniqueness — schema validation cannot reach the store.
	for _, row := range l.svcCtx.Profiles {
		other := row.(*types.Profile)
		if strings.EqualFold(other.Contacts.Email, req.Contacts.Email) {
			return nil, types.NewDuplicateEmailErr(types.DuplicateEmailBody{
				Code:  "DUPLICATE_EMAIL",
				Email: req.Contacts.Email,
			})
		}
	}

	// Demo rate limit — every 5th create raises 429 so the e2e suite can
	// exercise the path without time-sensitive flakes.
	l.svcCtx.NextID++
	if l.svcCtx.NextID%5 == 0 {
		l.svcCtx.NextID-- // don't burn the id when we reject
		return nil, types.NewRateLimitedErr(types.RateLimitedBody{
			Code:       "RATE_LIMITED",
			Message:    "Slow down, please",
			RetryAfter: 30,
		})
	}

	id := fmt.Sprintf("p%d", l.svcCtx.NextID)
	p := &types.Profile{
		ID:          id,
		DisplayName: req.DisplayName,
		Contacts:    req.Contacts,
		Addresses:   req.Addresses,
		Tags:        req.Tags,
		Meta:        req.Meta,
		Age:         req.Age,
	}
	l.svcCtx.Profiles[id] = p
	return p, nil
}

// isReserved blocks a small set of system-owned display names.
func isReserved(name string) bool {
	switch strings.ToLower(name) {
	case "admin", "root", "system":
		return true
	}
	return false
}

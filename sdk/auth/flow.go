package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/flow"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

var flowRefreshLead = 10 * time.Minute

// FlowAuthenticator handles authentication for Google Flow.
type FlowAuthenticator struct{}

// NewFlowAuthenticator constructs an authenticator for Google Flow.
func NewFlowAuthenticator() Authenticator {
	return &FlowAuthenticator{}
}

// Provider returns the identifier for Google Flow.
func (FlowAuthenticator) Provider() string {
	return "flow"
}

// RefreshLead returns the duration before expiry when refresh is recommended.
func (FlowAuthenticator) RefreshLead() *time.Duration {
	return &flowRefreshLead
}

// Login performs interactive or automated session capture for Google Flow.
func (a FlowAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if opts == nil {
		opts = &LoginOptions{}
	}
	var profile *flow.ChromeProfile
	if opts.Metadata != nil && opts.Metadata["profile_dir"] != "" {
		profile = &flow.ChromeProfile{
			ProfileDir: opts.Metadata["profile_dir"],
			Email:      opts.Metadata["email"],
			Name:       opts.Metadata["name"],
		}
	}
	auth, err := flow.HarvestProfile(ctx, cfg, profile, opts.NoBrowser)
	if err != nil {
		return nil, err
	}
	return BuildFlowAuthRecord(auth), nil
}

// BuildFlowAuthRecord converts a FlowAuth into a core Auth record.
func BuildFlowAuthRecord(resolved *flow.FlowAuth) *coreauth.Auth {
	if resolved == nil {
		return nil
	}
	tokenStorage := flow.NewTokenStorage(resolved)
	metadata := map[string]any{
		"type":          "flow",
		"access_token":  resolved.SessionToken,
		"session_token": resolved.SessionToken,
		"csrf_token":    resolved.CSRFToken,
		"email":         resolved.Email,
		"name":          resolved.Name,
		"profile_dir":   resolved.ProfileDir,
		"timestamp":     time.Now().UnixMilli(),
	}
	if tokenStorage != nil && tokenStorage.Expired != "" {
		metadata["expired"] = tokenStorage.Expired
	}
	var fileName string
	if strings.TrimSpace(resolved.Email) != "" {
		safe := SanitizeEmailForFilename(resolved.Email)
		fileName = fmt.Sprintf("flow-%s.json", safe)
	} else {
		fileName = fmt.Sprintf("flow-%d.json", time.Now().UnixMilli())
	}
	label := resolved.Name
	if label == "" {
		label = resolved.Email
	}
	if label == "" {
		label = "Google Flow Account"
	} else {
		label = fmt.Sprintf("Google Flow (%s)", label)
	}
	return &coreauth.Auth{
		ID:       fileName,
		Provider: "flow",
		FileName: fileName,
		Label:    label,
		Storage:  tokenStorage,
		Metadata: metadata,
		Attributes: map[string]string{
			"email":       resolved.Email,
			"profile_dir": resolved.ProfileDir,
		},
	}
}

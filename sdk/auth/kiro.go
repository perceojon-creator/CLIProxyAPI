package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/kiro"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// kiroRefreshLead is the duration before token expiry when refresh should occur.
var kiroRefreshLead = 5 * time.Minute

// KiroAuthenticator imports the Kiro desktop session as a CLIProxyAPI credential.
// Kiro has no browser OAuth flow of its own here: the desktop app (or an AWS SSO
// login) already wrote a bearer token to disk, so login reads and validates it.
type KiroAuthenticator struct{}

// NewKiroAuthenticator constructs a new Kiro authenticator.
func NewKiroAuthenticator() Authenticator {
	return &KiroAuthenticator{}
}

// Provider returns the provider key for kiro.
func (KiroAuthenticator) Provider() string {
	return "kiro"
}

// RefreshLead returns the duration before token expiry when refresh should occur.
func (KiroAuthenticator) RefreshLead() *time.Duration {
	return &kiroRefreshLead
}

// Login resolves the local Kiro session and turns it into an auth record.
func (a KiroAuthenticator) Login(_ context.Context, cfg *config.Config, _ *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}

	fmt.Println("Reading Kiro session...")
	resolved, err := kiro.ResolveKiroAuth(nil)
	if err != nil {
		return nil, err
	}

	tokenStorage := kiro.NewTokenStorage(resolved)
	metadata := map[string]any{
		"type":          "kiro",
		"access_token":  resolved.AccessToken,
		"refresh_token": resolved.RefreshToken,
		"region":        resolved.Region,
		"auth_method":   resolved.AuthMethod,
		"timestamp":     time.Now().UnixMilli(),
	}
	if strings.TrimSpace(resolved.ProfileArn) != "" {
		metadata["profile_arn"] = strings.TrimSpace(resolved.ProfileArn)
	}
	if tokenStorage != nil && tokenStorage.Expired != "" {
		metadata["expired"] = tokenStorage.Expired
	}

	fileName := fmt.Sprintf("kiro-%d.json", time.Now().UnixMilli())

	fmt.Println("Kiro session imported successfully!")

	return &coreauth.Auth{
		ID:       fileName,
		Provider: a.Provider(),
		FileName: fileName,
		Label:    kiroLabel(resolved),
		Storage:  tokenStorage,
		Metadata: metadata,
	}, nil
}

// kiroLabel derives a human-readable label from the resolved session.
func kiroLabel(resolved *kiro.KiroAuth) string {
	if resolved == nil {
		return "Kiro User"
	}
	method := strings.TrimSpace(resolved.AuthMethod)
	if method == "" {
		method = "social"
	}
	return fmt.Sprintf("Kiro User (%s/%s)", method, resolved.Region)
}

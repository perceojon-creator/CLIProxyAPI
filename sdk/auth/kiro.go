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

var kiroRefreshLead = 5 * time.Minute

type KiroAuthenticator struct{}

func NewKiroAuthenticator() Authenticator {
	return &KiroAuthenticator{}
}

func (KiroAuthenticator) Provider() string {
	return "kiro"
}

func (KiroAuthenticator) RefreshLead() *time.Duration {
	return &kiroRefreshLead
}

func (a KiroAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if opts == nil {
		opts = &LoginOptions{}
	}
	var resolved *kiro.KiroAuth
	var err error
	if !opts.NoBrowser {
		if opts.Metadata != nil && opts.Metadata["profile_dir"] != "" {
			profile := kiro.ChromeProfile{
				ProfileDir: opts.Metadata["profile_dir"],
				Email:      opts.Metadata["email"],
				Name:       opts.Metadata["name"],
			}
			resolved, err = kiro.LoginWithGoogleProfile(ctx, cfg, profile)
		} else {
			resolved, err = kiro.LoginWithGoogle(ctx, cfg, false)
		}
		if err != nil {
			resolved, err = kiro.ResolveKiroAuth(nil)
		}
	} else {
		fmt.Println("Reading Kiro session...")
		resolved, err = kiro.ResolveKiroAuth(nil)
	}
	if err != nil {
		return nil, err
	}
	rec := BuildKiroAuthRecord(resolved)
	fmt.Println("Kiro session imported successfully!")
	return rec, nil
}

func (a KiroAuthenticator) LoginWithProfile(ctx context.Context, cfg *config.Config, profile kiro.ChromeProfile) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	resolved, err := kiro.LoginWithGoogleProfile(ctx, cfg, profile)
	if err != nil {
		return nil, err
	}
	rec := BuildKiroAuthRecord(resolved)
	fmt.Println("Kiro session imported successfully!")
	return rec, nil
}

func BuildKiroAuthRecord(resolved *kiro.KiroAuth) *coreauth.Auth {
	if resolved == nil {
		return nil
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
	if strings.TrimSpace(resolved.Email) != "" {
		metadata["email"] = strings.TrimSpace(resolved.Email)
	}
	if strings.TrimSpace(resolved.ProfileArn) != "" {
		metadata["profile_arn"] = strings.TrimSpace(resolved.ProfileArn)
	}
	if tokenStorage != nil && tokenStorage.Expired != "" {
		metadata["expired"] = tokenStorage.Expired
	}
	var fileName string
	if strings.TrimSpace(resolved.Email) != "" {
		safe := SanitizeEmailForFilename(resolved.Email)
		fileName = fmt.Sprintf("kiro-%s.json", safe)
	} else {
		fileName = fmt.Sprintf("kiro-%d.json", time.Now().UnixMilli())
	}
	return &coreauth.Auth{
		ID:       fileName,
		Provider: "kiro",
		FileName: fileName,
		Label:    kiroLabel(resolved),
		Storage:  tokenStorage,
		Metadata: metadata,
	}
}

func SanitizeEmailForFilename(email string) string {
	r := strings.NewReplacer("@", "-", ".", "-", "+", "-")
	return r.Replace(strings.ToLower(strings.TrimSpace(email)))
}

func kiroLabel(resolved *kiro.KiroAuth) string {
	if resolved == nil {
		return "Kiro User"
	}
	if strings.TrimSpace(resolved.Email) != "" {
		return fmt.Sprintf("Kiro (%s)", resolved.Email)
	}
	method := strings.TrimSpace(resolved.AuthMethod)
	if method == "" {
		method = "social"
	}
	return fmt.Sprintf("Kiro User (%s/%s)", method, resolved.Region)
}

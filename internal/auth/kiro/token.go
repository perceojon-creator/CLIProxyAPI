package kiro

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
	log "github.com/sirupsen/logrus"
)

// refreshThresholdSeconds is the window before expiry within which the token is
// considered expired so a long turn cannot start on a credential that dies mid-stream.
const refreshThresholdSeconds = 60

// KiroTokenStorage stores Kiro session credentials for CLIProxyAPI auth files.
type KiroTokenStorage struct {
	// AccessToken is the bearer token used for Kiro runtime requests.
	AccessToken string `json:"access_token"`
	// RefreshToken is used to obtain a new access token.
	RefreshToken string `json:"refresh_token"`
	// ProfileArn identifies the CodeWhisperer profile bound to the session.
	ProfileArn string `json:"profile_arn,omitempty"`
	// Region is the AWS region hosting the Kiro runtime endpoint.
	Region string `json:"region,omitempty"`
	// AuthMethod is either "social" (Kiro desktop login) or "sso" (IAM Identity Center).
	AuthMethod string `json:"auth_method,omitempty"`
	// Expired is the RFC3339 timestamp when the access token expires.
	Expired string `json:"expired,omitempty"`
	// Email is the Google or AWS account email associated with the credential.
	Email string `json:"email,omitempty"`
	// Type indicates the authentication provider type, always "kiro" for this storage.
	Type string `json:"type"`

	// Metadata holds arbitrary key-value pairs injected via hooks.
	// It is not exported to JSON directly to allow flattening during serialization.
	Metadata map[string]any `json:"-"`
}

// SetMetadata allows external callers to inject metadata into the storage before saving.
func (ts *KiroTokenStorage) SetMetadata(meta map[string]any) {
	ts.Metadata = meta
}

// SaveTokenToFile serializes the Kiro token storage to a JSON file.
func (ts *KiroTokenStorage) SaveTokenToFile(authFilePath string) error {
	misc.LogSavingCredentials(authFilePath)
	ts.Type = "kiro"

	if err := os.MkdirAll(filepath.Dir(authFilePath), 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data, errMerge := misc.MergeMetadata(ts, ts.Metadata)
	if errMerge != nil {
		return fmt.Errorf("failed to merge metadata: %w", errMerge)
	}

	f, err := os.Create(authFilePath)
	if err != nil {
		return fmt.Errorf("failed to create token file: %w", err)
	}
	defer func() {
		if errClose := f.Close(); errClose != nil {
			log.Errorf("kiro token storage: close token file error: %v", errClose)
		}
	}()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(data); err != nil {
		return fmt.Errorf("failed to write token to file: %w", err)
	}
	return nil
}

// IsExpired reports whether the stored access token is expired or about to expire.
func (ts *KiroTokenStorage) IsExpired() bool {
	if ts.Expired == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, ts.Expired)
	if err != nil {
		return true
	}
	return time.Now().Add(refreshThresholdSeconds * time.Second).After(t)
}

// NeedsRefresh reports whether the token should be refreshed before the next request.
func (ts *KiroTokenStorage) NeedsRefresh() bool {
	if ts.RefreshToken == "" {
		return false
	}
	return ts.IsExpired()
}

// NewTokenStorage builds a token storage from resolved Kiro credentials.
func NewTokenStorage(auth *KiroAuth) *KiroTokenStorage {
	if auth == nil {
		return nil
	}
	ts := &KiroTokenStorage{
		AccessToken:  auth.AccessToken,
		RefreshToken: auth.RefreshToken,
		ProfileArn:   auth.ProfileArn,
		Region:       auth.Region,
		AuthMethod:   auth.AuthMethod,
		Email:        auth.Email,
		Type:         "kiro",
	}
	if auth.ExpiresAt > 0 {
		ts.Expired = time.UnixMilli(auth.ExpiresAt).UTC().Format(time.RFC3339)
	}
	return ts
}

package flow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
	log "github.com/sirupsen/logrus"
)

const refreshThresholdSeconds = 300

// FlowTokenStorage stores Google Flow session credentials for CLIProxyAPI auth files.
type FlowTokenStorage struct {
	SessionToken string         `json:"session_token"`
	CSRFToken    string         `json:"csrf_token,omitempty"`
	Email        string         `json:"email,omitempty"`
	Name         string         `json:"name,omitempty"`
	ProfileDir   string         `json:"profile_dir,omitempty"`
	Expired      string         `json:"expired,omitempty"`
	Type         string         `json:"type"`
	Metadata     map[string]any `json:"-"`
}

// SetMetadata allows external callers to inject metadata into storage before saving.
func (ts *FlowTokenStorage) SetMetadata(meta map[string]any) {
	ts.Metadata = meta
}

// SaveTokenToFile serializes the Flow token storage to a JSON file.
func (ts *FlowTokenStorage) SaveTokenToFile(authFilePath string) error {
	misc.LogSavingCredentials(authFilePath)
	ts.Type = "flow"

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
			log.Errorf("flow token storage: close token file error: %v", errClose)
		}
	}()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(data); err != nil {
		return fmt.Errorf("failed to write token to file: %w", err)
	}
	return nil
}

// IsExpired reports whether the stored session token is expired or about to expire.
func (ts *FlowTokenStorage) IsExpired() bool {
	if ts.Expired == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, ts.Expired)
	if err != nil {
		return false
	}
	return time.Now().Add(refreshThresholdSeconds * time.Second).After(t)
}

// NeedsRefresh reports whether the token should be refreshed.
func (ts *FlowTokenStorage) NeedsRefresh() bool {
	return ts.IsExpired()
}

// NewTokenStorage builds a token storage from resolved Flow credentials.
func NewTokenStorage(auth *FlowAuth) *FlowTokenStorage {
	if auth == nil {
		return nil
	}
	ts := &FlowTokenStorage{
		SessionToken: auth.SessionToken,
		CSRFToken:    auth.CSRFToken,
		Email:        auth.Email,
		Name:         auth.Name,
		ProfileDir:   auth.ProfileDir,
		Type:         "flow",
	}
	if auth.ExpiresAt > 0 {
		ts.Expired = time.UnixMilli(auth.ExpiresAt).UTC().Format(time.RFC3339)
	}
	return ts
}

// ResolveFlowAuth reads and parses a Flow credentials JSON file from disk.
func ResolveFlowAuth(path string) (*FlowAuth, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading flow auth file: %w", err)
	}
	var storage FlowTokenStorage
	if err := json.Unmarshal(data, &storage); err != nil {
		return nil, fmt.Errorf("unmarshaling flow auth: %w", err)
	}
	if strings.TrimSpace(storage.SessionToken) == "" {
		return nil, fmt.Errorf("flow auth file %s has no session_token", path)
	}
	var expiresAt int64
	if storage.Expired != "" {
		if t, err := time.Parse(time.RFC3339, storage.Expired); err == nil {
			expiresAt = t.UnixMilli()
		}
	}
	return &FlowAuth{
		SessionToken: storage.SessionToken,
		CSRFToken:    storage.CSRFToken,
		Email:        storage.Email,
		Name:         storage.Name,
		ProfileDir:   storage.ProfileDir,
		ExpiresAt:    expiresAt,
		UpdatedAt:    time.Now().UnixMilli(),
	}, nil
}

// Package kiro provides authentication and token management for the Kiro AI service.
// Kiro keeps a bearer token in ~/.aws/sso/cache/kiro-auth-token.json and refreshes it
// roughly every 50 minutes. This package reads that file directly so no Kiro IDE or
// kiro-cli is required at request time.
package kiro

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	defaultRegion = "us-east-1"
	// expirySkewMS is the number of milliseconds before actual expiry to treat a token as expired,
	// so a long turn cannot start on a token that dies mid-stream.
	expirySkewMS = 60_000
)

// KiroAuth holds Kiro desktop session credentials.
type KiroAuth struct {
	AccessToken  string
	RefreshToken string
	ProfileArn   string
	Region       string
	ExpiresAt    int64  // Unix milliseconds
	AuthMethod   string // "social" (Kiro desktop login) or "sso" (IAM Identity Center)
}

type kiroTokenFile struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn"`
	ExpiresAt    string `json:"expiresAt"`
	Region       string `json:"region"`
	AuthMethod   string `json:"authMethod"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// KiroTokenPath returns the path to the Kiro desktop app token file.
func KiroTokenPath() string {
	if p := os.Getenv("KIRO_CREDS_FILE"); p != "" {
		return p
	}
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".aws", "sso", "cache", "kiro-auth-token.json")
}

// kiroCachePath returns the path where CLIProxyAPI stores its own copy of Kiro credentials.
func kiroCachePath() string {
	homeDir, _ := os.UserHomeDir()
	var dir string
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(homeDir, "AppData", "Roaming")
		}
		dir = filepath.Join(appData, "CLIProxyAPI", "kiro")
	case "darwin":
		dir = filepath.Join(homeDir, "Library", "Application Support", "CLIProxyAPI", "kiro")
	default:
		dir = filepath.Join(homeDir, ".local", "share", "CLIProxyAPI", "kiro")
	}
	return filepath.Join(dir, "kiro-session.json")
}

// IsExpired reports whether the token is expired (with a 60-second skew).
func (a *KiroAuth) IsExpired() bool {
	if a.ExpiresAt == 0 {
		return true
	}
	return a.ExpiresAt-expirySkewMS <= time.Now().UnixMilli()
}

// RefreshEndpoint returns the appropriate token refresh URL based on auth method.
func (a *KiroAuth) RefreshEndpoint() string {
	if a.AuthMethod == "sso" {
		return fmt.Sprintf("https://oidc.%s.amazonaws.com/token", a.Region)
	}
	return fmt.Sprintf("https://prod.%s.auth.desktop.kiro.dev/refreshToken", a.Region)
}

var profileArnRegion = regexp.MustCompile(`^arn:aws:[^:]+:([^:]+):`)

func regionFromProfileArn(arn string) string {
	if m := profileArnRegion.FindStringSubmatch(arn); len(m) > 1 {
		return m[1]
	}
	return ""
}

func parseTokenFile(raw []byte) *KiroAuth {
	var f kiroTokenFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil
	}
	if f.AccessToken == "" || f.RefreshToken == "" {
		return nil
	}
	region := f.Region
	if region == "" {
		region = regionFromProfileArn(f.ProfileArn)
	}
	if region == "" {
		region = defaultRegion
	}
	// The desktop login has no clientId/clientSecret; AWS SSO does.
	authMethod := "social"
	if f.ClientID != "" && f.ClientSecret != "" {
		authMethod = "sso"
	}
	if f.AuthMethod != "" {
		authMethod = f.AuthMethod
	}
	var expiresAt int64
	if f.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, f.ExpiresAt); err == nil {
			expiresAt = t.UnixMilli()
		}
	}
	return &KiroAuth{
		AccessToken:  f.AccessToken,
		RefreshToken: f.RefreshToken,
		ProfileArn:   f.ProfileArn,
		Region:       region,
		ExpiresAt:    expiresAt,
		AuthMethod:   authMethod,
	}
}

func readFileAuth(path string) *KiroAuth {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseTokenFile(data)
}

// ReadKiroAuth returns the best available Kiro credentials.
// The Kiro desktop app file wins when present; the cache file is the fallback.
func ReadKiroAuth() *KiroAuth {
	fromApp := readFileAuth(KiroTokenPath())
	fromCache := readFileAuth(kiroCachePath())
	if fromApp != nil && fromCache != nil {
		if fromApp.ExpiresAt >= fromCache.ExpiresAt {
			return fromApp
		}
		return fromCache
	}
	if fromApp != nil {
		return fromApp
	}
	return fromCache
}

// WriteKiroCache persists credentials to the local CLIProxyAPI cache using an atomic write.
func WriteKiroCache(auth *KiroAuth) {
	path := kiroCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		log.Warnf("kiro: failed to create cache directory: %v", err)
		return
	}
	payload := kiroTokenFile{
		AccessToken:  auth.AccessToken,
		RefreshToken: auth.RefreshToken,
		ProfileArn:   auth.ProfileArn,
		Region:       auth.Region,
		ExpiresAt:    time.UnixMilli(auth.ExpiresAt).UTC().Format(time.RFC3339),
		AuthMethod:   auth.AuthMethod,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		log.Warnf("kiro: failed to write cache file: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Warnf("kiro: failed to rename cache file: %v", err)
	}
}

// RefreshKiroAuth exchanges the refresh token for a new access token and caches the result.
func RefreshKiroAuth(auth *KiroAuth, client *http.Client) (*KiroAuth, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	bodyStr := fmt.Sprintf(`{"refreshToken":%q}`, auth.RefreshToken)
	req, err := http.NewRequest(http.MethodPost, auth.RefreshEndpoint(), strings.NewReader(bodyStr))
	if err != nil {
		return nil, fmt.Errorf("kiro: build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kiro: refresh request failed: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("kiro: close refresh response body: %v", errClose)
		}
	}()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kiro: read refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		preview := string(raw)
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return nil, fmt.Errorf("kiro: token refresh failed (%d): %s — sign in again in the Kiro app", resp.StatusCode, strings.TrimSpace(preview))
	}

	var data struct {
		AccessToken   string `json:"accessToken"`
		AccessTokenS  string `json:"access_token"`
		RefreshToken  string `json:"refreshToken"`
		RefreshTokenS string `json:"refresh_token"`
		ProfileArn    string `json:"profileArn"`
		ExpiresAt     string `json:"expiresAt"`
		ExpiresIn     int64  `json:"expiresIn"`
		ExpiresInS    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("kiro: parse refresh response: %w", err)
	}

	accessToken := data.AccessToken
	if accessToken == "" {
		accessToken = data.AccessTokenS
	}
	if accessToken == "" {
		return nil, fmt.Errorf("kiro: refresh returned no access token")
	}

	refreshToken := data.RefreshToken
	if refreshToken == "" {
		refreshToken = data.RefreshTokenS
	}
	if refreshToken == "" {
		refreshToken = auth.RefreshToken
	}

	profileArn := data.ProfileArn
	if profileArn == "" {
		profileArn = auth.ProfileArn
	}

	var expiresAt int64
	if data.ExpiresAt != "" {
		if t, err := time.Parse(time.RFC3339, data.ExpiresAt); err == nil {
			expiresAt = t.UnixMilli()
		}
	}
	if expiresAt == 0 {
		expiresInSec := data.ExpiresIn
		if expiresInSec == 0 {
			expiresInSec = data.ExpiresInS
		}
		if expiresInSec == 0 {
			expiresInSec = 3600
		}
		expiresAt = time.Now().Add(time.Duration(expiresInSec) * time.Second).UnixMilli()
	}

	next := &KiroAuth{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ProfileArn:   profileArn,
		Region:       auth.Region,
		ExpiresAt:    expiresAt,
		AuthMethod:   auth.AuthMethod,
	}
	WriteKiroCache(next)
	return next, nil
}

// ResolveKiroAuth returns usable Kiro credentials, refreshing them when expired.
// Refreshing rotates the token the Kiro app also uses, so it only happens when the
// stored token is actually expired.
func ResolveKiroAuth(client *http.Client) (*KiroAuth, error) {
	auth := ReadKiroAuth()
	if auth == nil {
		return nil, fmt.Errorf("kiro: no session found at %s — install the Kiro app and sign in, or set KIRO_CREDS_FILE", KiroTokenPath())
	}
	if !auth.IsExpired() {
		return auth, nil
	}
	return RefreshKiroAuth(auth, client)
}

// HasKiroSession reports whether any Kiro credentials are readable on this machine.
func HasKiroSession() bool {
	return ReadKiroAuth() != nil
}

package copilot

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	log "github.com/sirupsen/logrus"
)

// TokenResponse represents OAuth token response from GitHub OAuth.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error,omitempty"`
	ErrorDesc   string `json:"error_description,omitempty"`
}

// UserProfile represents GitHub user profile from /user.
type UserProfile struct {
	Login string `json:"login"`
	Email string `json:"email"`
	ID    int64  `json:"id"`
	Name  string `json:"name"`
}

// CopilotSessionToken represents the internal session token returned by /copilot_internal/v2/token.
type CopilotSessionToken struct {
	Token         string            `json:"token"`
	ExpiresAt     int64             `json:"expires_at"`
	RefreshIn     int64             `json:"refresh_in"`
	Endpoints     map[string]string `json:"endpoints"`
	ChatEnabled   bool              `json:"chat_enabled"`
	AccessTypeSKU string            `json:"access_type_sku"`
}

// CopilotUserResponse represents the subscription details returned by /copilot_internal/user.
type CopilotUserResponse struct {
	Login         string `json:"login"`
	AccessTypeSKU string `json:"access_type_sku"`
	ChatEnabled   bool   `json:"chat_enabled"`
	CopilotPlan   string `json:"copilot_plan"`
	Endpoints     struct {
		API           string `json:"api"`
		Proxy         string `json:"proxy"`
		Telemetry     string `json:"telemetry"`
		OriginTracker string `json:"origin-tracker"`
	} `json:"endpoints"`
}

// CopilotAuth handles Copilot OAuth and subscription session authentication.
type CopilotAuth struct {
	cfg        *config.Config
	httpClient *http.Client
}

// NewCopilotAuth creates a new Copilot auth service.
func NewCopilotAuth(cfg *config.Config, httpClient *http.Client) *CopilotAuth {
	if cfg == nil {
		cfg = &config.Config{}
	}
	if httpClient != nil {
		return &CopilotAuth{cfg: cfg, httpClient: httpClient}
	}
	return &CopilotAuth{
		cfg:        cfg,
		httpClient: util.SetProxy(&cfg.SDKConfig, &http.Client{Timeout: 30 * time.Second}),
	}
}

// GeneratePKCE creates a cryptographic code_verifier and code_challenge (S256).
func GeneratePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", fmt.Errorf("copilot: generate PKCE random bytes: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return verifier, challenge, nil
}

// BuildAuthURL constructs the GitHub OAuth authorization URL with PKCE.
func (a *CopilotAuth) BuildAuthURL(state, redirectURI, codeChallenge string) string {
	q := url.Values{}
	q.Set("client_id", ClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", DefaultOAuthScopes)
	q.Set("state", state)
	if codeChallenge != "" {
		q.Set("code_challenge", codeChallenge)
		q.Set("code_challenge_method", "S256")
	}
	return fmt.Sprintf("%s?%s", AuthEndpoint, q.Encode())
}

// DeviceCodeResponse represents the device authorization response from GitHub.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// StartDeviceFlow initiates GitHub Device Flow for Copilot.
func (a *CopilotAuth) StartDeviceFlow(ctx context.Context) (*DeviceCodeResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	payload := map[string]string{
		"client_id": ClientID,
		"scope":     DefaultOAuthScopes,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("copilot: marshal device flow payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, DeviceEndpoint, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("copilot: create device flow request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", DefaultUserAgent)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot: device flow failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("copilot: device flow returned status %d: %s", resp.StatusCode, string(body))
	}
	var dCR DeviceCodeResponse
	if err = json.NewDecoder(resp.Body).Decode(&dCR); err != nil {
		return nil, fmt.Errorf("copilot: decode device code: %w", err)
	}
	return &dCR, nil
}

// WaitForDeviceAuthorization polls GitHub until the user approves the device code.
func (a *CopilotAuth) WaitForDeviceAuthorization(ctx context.Context, dcr *DeviceCodeResponse) (*TokenResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	interval := dcr.Interval
	if interval < 5 {
		interval = 5
	}
	deadline := time.Now().Add(time.Duration(dcr.ExpiresIn) * time.Second)
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("copilot: device code expired")
			}
			payload := map[string]string{
				"client_id":   ClientID,
				"device_code": dcr.DeviceCode,
				"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
			}
			b, _ := json.Marshal(payload)
			req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, TokenEndpoint, bytes.NewReader(b))
			if errReq != nil {
				return nil, errReq
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")
			req.Header.Set("User-Agent", DefaultUserAgent)

			resp, errResp := a.httpClient.Do(req)
			if errResp != nil {
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()

			var tResp TokenResponse
			_ = json.Unmarshal(body, &tResp)
			if tResp.AccessToken != "" {
				return &tResp, nil
			}
			if tResp.Error == "authorization_pending" {
				continue
			}
			if tResp.Error == "slow_down" {
				interval += 5
				ticker.Reset(time.Duration(interval) * time.Second)
				continue
			}
			if tResp.Error != "" {
				return nil, fmt.Errorf("copilot: %s (%s)", tResp.Error, tResp.ErrorDesc)
			}
		}
	}
}

// ExchangeCodeForTokens exchanges an authorization code for an OAuth access token.
func (a *CopilotAuth) ExchangeCodeForTokens(ctx context.Context, code, redirectURI, codeVerifier string) (*TokenResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	data := url.Values{}
	data.Set("client_id", ClientID)
	data.Set("client_secret", ClientSecret)
	data.Set("code", strings.TrimSpace(code))
	data.Set("redirect_uri", strings.TrimSpace(redirectURI))
	if codeVerifier != "" {
		data.Set("code_verifier", strings.TrimSpace(codeVerifier))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("copilot: create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", DefaultUserAgent)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot: token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("copilot: read token response: %w", err)
	}

	var tokenResp TokenResponse
	if err = json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("copilot: decode token response: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("copilot: token error: %s (%s)", tokenResp.Error, tokenResp.ErrorDesc)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("copilot: empty access token returned")
	}
	return &tokenResp, nil
}

// FetchUserInfo fetches the user profile from GitHub API (/user).
func (a *CopilotAuth) FetchUserInfo(ctx context.Context, token string) (*UserProfile, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, UserInfoEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("copilot: create user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", DefaultUserAgent)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot: fetch user info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot: fetch user returned status %d", resp.StatusCode)
	}
	var profile UserProfile
	if err = json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("copilot: decode user profile: %w", err)
	}
	return &profile, nil
}

// FetchCopilotUser queries subscription SKU and dynamic endpoints from /copilot_internal/user.
func (a *CopilotAuth) FetchCopilotUser(ctx context.Context, token string) (*CopilotUserResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CopilotInternalUserURL, nil)
	if err != nil {
		return nil, fmt.Errorf("copilot: create copilot user request: %w", err)
	}
	req.Header.Set("Authorization", "token "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", DefaultUserAgent)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot: fetch copilot user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot: copilot internal user returned status %d", resp.StatusCode)
	}
	var res CopilotUserResponse
	if err = json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("copilot: decode copilot user response: %w", err)
	}
	if res.AccessTypeSKU == "no_access" {
		_ = a.SubscribeLimitedUser(ctx, token)
		if retryRes, errRetry := a.fetchCopilotUserRaw(ctx, token); errRetry == nil && retryRes != nil {
			return retryRes, nil
		}
	}
	return &res, nil
}

func (a *CopilotAuth) fetchCopilotUserRaw(ctx context.Context, token string) (*CopilotUserResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CopilotInternalUserURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", DefaultUserAgent)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var res CopilotUserResponse
	if err = json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

// SubscribeLimitedUser enrolls a GitHub user into the free limited Copilot tier if eligible.
func (a *CopilotAuth) SubscribeLimitedUser(ctx context.Context, token string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	body := []byte(`{"restricted_telemetry":"enabled","public_code_suggestions":"enabled"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com/copilot_internal/subscribe_limited_user", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", DefaultUserAgent)
	req.Header.Set("X-GitHub-Api-Version", "2025-05-01")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// FetchCopilotSession exchanges a GitHub token for a short-lived Copilot session token from /copilot_internal/v2/token.
func (a *CopilotAuth) FetchCopilotSession(ctx context.Context, token string) (*CopilotSessionToken, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CopilotInternalTokenURL, nil)
	if err != nil {
		return nil, fmt.Errorf("copilot: create session token request: %w", err)
	}
	req.Header.Set("Authorization", "token "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Editor-Version", DefaultEditorVersion)
	req.Header.Set("Editor-Plugin-Version", DefaultEditorPluginVersion)
	req.Header.Set("User-Agent", DefaultUserAgent)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("copilot: fetch session token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot: session token returned status %d", resp.StatusCode)
	}
	var session CopilotSessionToken
	if err = json.NewDecoder(resp.Body).Decode(&session); err != nil {
		return nil, fmt.Errorf("copilot: decode session token: %w", err)
	}
	return &session, nil
}

// DetectLocalCredentials checks existing local Copilot CLI, GitHub CLI, and Windows Credential Manager.
func DetectLocalCredentials(ctx context.Context, cfg *config.Config) (token, user, sku, baseURL string, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	authSvc := NewCopilotAuth(cfg, nil)

	// 1. Environment variables
	for _, env := range []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		if val := strings.TrimSpace(os.Getenv(env)); val != "" {
			if u, s, b, ok := validateToken(ctx, authSvc, val); ok {
				log.Infof("copilot: found valid credentials in environment variable %s", env)
				return val, u, s, b, nil
			}
		}
	}

	// 2. Windows Credential Manager
	if runtime.GOOS == "windows" {
		if t, u := readWindowsKeyring(ctx); t != "" {
			if login, s, b, ok := validateToken(ctx, authSvc, t); ok {
				if login == "" {
					login = u
				}
				log.Infof("copilot: found valid credentials in Windows Credential Manager for %s", login)
				return t, login, s, b, nil
			}
		}
	}

	// 3. ~/.config/github-copilot/hosts.json or AppData
	if home, errHome := os.UserHomeDir(); errHome == nil {
		candidates := []string{
			filepath.Join(home, ".config", "github-copilot", "hosts.json"),
			filepath.Join(home, "AppData", "Local", "github-copilot", "hosts.json"),
			filepath.Join(home, "AppData", "Roaming", "github-copilot", "hosts.json"),
		}
		for _, c := range candidates {
			if t, u := readHostsFile(c); t != "" {
				if login, s, b, ok := validateToken(ctx, authSvc, t); ok {
					if login == "" {
						login = u
					}
					log.Infof("copilot: found valid credentials in %s for %s", c, login)
					return t, login, s, b, nil
				}
			}
		}
	}

	// 4. GitHub CLI (gh auth token)
	if cmd := exec.CommandContext(ctx, "gh", "auth", "token"); cmd != nil {
		if out, errGH := cmd.Output(); errGH == nil {
			t := strings.TrimSpace(string(out))
			if t != "" {
				if login, s, b, ok := validateToken(ctx, authSvc, t); ok {
					log.Infof("copilot: found valid credentials in GitHub CLI (gh) for %s", login)
					return t, login, s, b, nil
				}
			}
		}
	}

	return "", "", "", "", fmt.Errorf("copilot: no active local credentials found")
}

func validateToken(ctx context.Context, authSvc *CopilotAuth, token string) (user, sku, baseURL string, ok bool) {
	baseURL = DefaultUpstreamBaseURL
	copilotUser, errCU := authSvc.FetchCopilotUser(ctx, token)
	if errCU == nil && copilotUser != nil {
		user = copilotUser.Login
		sku = copilotUser.AccessTypeSKU
		if copilotUser.Endpoints.API != "" {
			baseURL = copilotUser.Endpoints.API
		}
		return user, sku, baseURL, true
	}
	userProfile, errUP := authSvc.FetchUserInfo(ctx, token)
	if errUP == nil && userProfile != nil {
		user = userProfile.Login
		return user, sku, baseURL, true
	}
	return "", "", "", false
}

func readHostsFile(path string) (string, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	var hosts map[string]struct {
		OAuthToken string `json:"oauth_token"`
		User       string `json:"user"`
	}
	if err = json.Unmarshal(data, &hosts); err != nil {
		return "", ""
	}
	for _, h := range hosts {
		if strings.TrimSpace(h.OAuthToken) != "" {
			return strings.TrimSpace(h.OAuthToken), strings.TrimSpace(h.User)
		}
	}
	return "", ""
}

func readWindowsKeyring(ctx context.Context) (string, string) {
	if runtime.GOOS != "windows" {
		return "", ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	psScript := `$source = @"
using System;
using System.Runtime.InteropServices;
using System.Text;
public class CredMgr {
    [DllImport("advapi32.dll", EntryPoint = "CredReadW", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern bool CredRead(string target, int type, int reservedFlag, out IntPtr credentialPtr);
    [DllImport("advapi32.dll", EntryPoint = "CredFree", SetLastError = true)]
    public static extern void CredFree(IntPtr credential);
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    public struct CREDENTIAL {
        public int Flags;
        public int Type;
        public string TargetName;
        public string Comment;
        public System.Runtime.InteropServices.ComTypes.FILETIME LastWritten;
        public int CredentialBlobSize;
        public IntPtr CredentialBlob;
        public int Persist;
        public int AttributeCount;
        public IntPtr Attributes;
        public string TargetAlias;
        public string UserName;
    }
    public static string Read(string target) {
        IntPtr ptr;
        if (CredRead(target, 1, 0, out ptr)) {
            CREDENTIAL c = (CREDENTIAL)Marshal.PtrToStructure(ptr, typeof(CREDENTIAL));
            byte[] b = new byte[c.CredentialBlobSize];
            Marshal.Copy(c.CredentialBlob, b, 0, c.CredentialBlobSize);
            CredFree(ptr);
            return Encoding.Unicode.GetString(b);
        }
        return null;
    }
}
"@
Add-Type -TypeDefinition $source -ErrorAction SilentlyContinue
$lines = cmdkey /list | Select-String "target=(.*copilot-cli)"
foreach ($line in $lines) {
    if ($line.Matches[0].Groups[1].Value) {
        $t = $line.Matches[0].Groups[1].Value
        $val = [CredMgr]::Read($t)
        if ($val) { Write-Output "$t:::$val"; break }
    }
}`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", psScript)
	out, err := cmd.Output()
	if err != nil {
		return "", ""
	}
	result := strings.TrimSpace(string(out))
	if idx := strings.Index(result, ":::"); idx != -1 {
		target := result[:idx]
		token := strings.TrimSpace(result[idx+3:])
		login := ""
		if strings.HasPrefix(target, "https://github.com:") && strings.HasSuffix(target, ".copilot-cli") {
			login = strings.TrimSuffix(strings.TrimPrefix(target, "https://github.com:"), ".copilot-cli")
		}
		return token, login
	}
	return "", ""
}

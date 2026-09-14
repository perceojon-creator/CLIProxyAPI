package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
)

// VerifyFlowSession validates a NextAuth session token against labs.google.
func VerifyFlowSession(ctx context.Context, sessionToken string, cfg *config.Config) (*FlowUserInfo, error) {
	sessionToken = strings.TrimSpace(sessionToken)
	if sessionToken == "" {
		return nil, fmt.Errorf("flow session: session token is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	if cfg != nil {
		httpClient = util.SetProxy(&cfg.SDKConfig, httpClient)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FlowSessionEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("flow session: create request: %w", err)
	}
	req.Header.Set("Cookie", fmt.Sprintf("%s=%s", SessionCookieName, sessionToken))
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", FlowAppURL)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("flow session: execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("flow session: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("flow session: upstream returned status %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		User struct {
			Name  string `json:"name"`
			Email string `json:"email"`
			Image string `json:"image"`
		} `json:"user"`
		Expires string `json:"expires"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("flow session: parse response: %w", err)
	}

	if strings.TrimSpace(data.User.Email) == "" {
		return nil, fmt.Errorf("flow session: unauthenticated (no user session found on labs.google)")
	}

	return &FlowUserInfo{
		Name:    strings.TrimSpace(data.User.Name),
		Email:   strings.TrimSpace(data.User.Email),
		Image:   strings.TrimSpace(data.User.Image),
		Expires: strings.TrimSpace(data.Expires),
	}, nil
}

// FetchFlowCSRF retrieves a valid CSRF token and cookie from labs.google.
func FetchFlowCSRF(ctx context.Context, sessionToken string, cfg *config.Config) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	if cfg != nil {
		httpClient = util.SetProxy(&cfg.SDKConfig, httpClient)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FlowCSRFEndpoint, nil)
	if err != nil {
		return "", "", fmt.Errorf("flow csrf: create request: %w", err)
	}
	if strings.TrimSpace(sessionToken) != "" {
		req.Header.Set("Cookie", fmt.Sprintf("%s=%s", SessionCookieName, strings.TrimSpace(sessionToken)))
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", FlowAppURL)

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("flow csrf: execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("flow csrf: read body: %w", err)
	}

	var csrfResp struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.Unmarshal(body, &csrfResp)

	csrfCookie := ""
	for _, c := range resp.Cookies() {
		if c.Name == CSRFCookieName {
			csrfCookie = c.Value
			break
		}
	}

	token := csrfResp.CSRFToken
	if token == "" {
		token = csrfCookie
	}
	return token, csrfCookie, nil
}

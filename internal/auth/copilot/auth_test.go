package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestCredentialFileName(t *testing.T) {
	if got := CredentialFileName(""); got != "copilot.json" {
		t.Errorf("expected copilot.json, got %s", got)
	}
	if got := CredentialFileName("octocat"); got != "copilot-octocat.json" {
		t.Errorf("expected copilot-octocat.json, got %s", got)
	}
	if got := CredentialFileName("user@domain.com"); got != "copilot-user-domain-com.json" {
		t.Errorf("expected copilot-user-domain-com.json, got %s", got)
	}
}

func TestGeneratePKCE(t *testing.T) {
	v1, c1, err1 := GeneratePKCE()
	if err1 != nil {
		t.Fatalf("GeneratePKCE failed: %v", err1)
	}
	if len(v1) == 0 || len(c1) == 0 {
		t.Errorf("expected non-empty verifier and challenge")
	}
	v2, c2, _ := GeneratePKCE()
	if v1 == v2 || c1 == c2 {
		t.Errorf("expected unique PKCE pairs across invocations")
	}
}

func TestBuildAuthURL(t *testing.T) {
	authSvc := NewCopilotAuth(&config.Config{}, nil)
	url := authSvc.BuildAuthURL("state123", "http://localhost:51122/oauth-callback", "chal456")
	if !strings.Contains(url, "client_id="+ClientID) {
		t.Errorf("URL missing client_id: %s", url)
	}
	if !strings.Contains(url, "code_challenge=chal456") {
		t.Errorf("URL missing code_challenge: %s", url)
	}
	if !strings.Contains(url, "state=state123") {
		t.Errorf("URL missing state: %s", url)
	}
}

func TestFetchCopilotUserMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "token gho_test") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"login":           "testuser",
			"access_type_sku": "individual",
			"chat_enabled":    true,
			"endpoints": map[string]string{
				"api": "https://api.individual.githubcopilot.com",
			},
		})
	}))
	defer srv.Close()

	authSvc := &CopilotAuth{
		cfg:        &config.Config{},
		httpClient: srv.Client(),
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	req.Header.Set("Authorization", "token gho_test")
	resp, err := authSvc.httpClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var user CopilotUserResponse
	if err = json.NewDecoder(resp.Body).Decode(&user); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if user.Login != "testuser" || user.AccessTypeSKU != "individual" {
		t.Errorf("unexpected user: %+v", user)
	}
}

func TestDetectLocalCredentialsOnHost(t *testing.T) {
	cfg := &config.Config{}
	token, user, sku, baseURL, err := DetectLocalCredentials(context.Background(), cfg)
	if err != nil {
		t.Logf("no local credentials: %v", err)
		return
	}
	t.Logf("Found local credentials on host: user=%s, sku=%s, baseURL=%s, tokenLen=%d", user, sku, baseURL, len(token))
	if token == "" {
		t.Errorf("expected non-empty token")
	}
}

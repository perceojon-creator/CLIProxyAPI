package flow

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFlowTokenStorage_Lifecycle(t *testing.T) {
	tempDir := t.TempDir()
	savePath := filepath.Join(tempDir, "flow-test@gmail.com.json")

	auth := &FlowAuth{
		SessionToken: "sample-session-token-12345",
		CSRFToken:    "sample-csrf-token-67890",
		Email:        "test@gmail.com",
		Name:         "Tester",
		ProfileDir:   "Profile 1",
		ExpiresAt:    time.Now().Add(24 * time.Hour).UnixMilli(),
		UpdatedAt:    time.Now().UnixMilli(),
	}

	storage := NewTokenStorage(auth)
	if storage == nil {
		t.Fatal("expected non-nil token storage")
	}
	if storage.Type != "flow" {
		t.Fatalf("expected storage type flow, got %s", storage.Type)
	}
	if storage.IsExpired() {
		t.Fatal("expected unexpired token storage")
	}

	if err := storage.SaveTokenToFile(savePath); err != nil {
		t.Fatalf("SaveTokenToFile failed: %v", err)
	}

	resolved, err := ResolveFlowAuth(savePath)
	if err != nil {
		t.Fatalf("ResolveFlowAuth failed: %v", err)
	}
	if resolved.SessionToken != "sample-session-token-12345" {
		t.Fatalf("expected token sample-session-token-12345, got %s", resolved.SessionToken)
	}
	if resolved.Email != "test@gmail.com" {
		t.Fatalf("expected email test@gmail.com, got %s", resolved.Email)
	}

	cookieHeader := resolved.CookieHeader()
	wantCookie := "__Secure-next-auth.session-token=sample-session-token-12345; __Host-next-auth.csrf-token=sample-csrf-token-67890"
	if cookieHeader != wantCookie {
		t.Fatalf("cookie header mismatch: got %q, want %q", cookieHeader, wantCookie)
	}
}

func TestFlowAuth_IsExpired(t *testing.T) {
	expiredAuth := &FlowAuth{
		SessionToken: "token",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).UnixMilli(),
	}
	if !expiredAuth.IsExpired() {
		t.Fatal("expected expired auth to report true")
	}

	emptyAuth := &FlowAuth{}
	if !emptyAuth.IsExpired() {
		t.Fatal("expected empty auth to report true")
	}
}

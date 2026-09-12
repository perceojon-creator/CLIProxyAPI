package flow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestVerifyFlowSession_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "__Secure-next-auth.session-token=valid-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user":{"name":"Perceo Jon","email":"perceojon@gmail.com","image":"https://example.com/p.jpg"},"expires":"2026-10-12T00:00:00.000Z"}`))
	}))
	defer srv.Close()

	// Test using custom client pointing to mock server
	origEndpoint := FlowSessionEndpoint
	defer func() { FlowSessionEndpoint = origEndpoint }()
	FlowSessionEndpoint = srv.URL

	info, err := VerifyFlowSession(context.Background(), "valid-token", nil)
	if err != nil {
		t.Fatalf("expected verify success, got error: %v", err)
	}
	if info.Email != "perceojon@gmail.com" {
		t.Fatalf("expected perceojon@gmail.com, got %s", info.Email)
	}
	if info.Name != "Perceo Jon" {
		t.Fatalf("expected Perceo Jon, got %s", info.Name)
	}
}

func TestVerifyFlowSession_Unauthenticated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	origEndpoint := FlowSessionEndpoint
	defer func() { FlowSessionEndpoint = origEndpoint }()
	FlowSessionEndpoint = srv.URL

	_, err := VerifyFlowSession(context.Background(), "empty-user-token", nil)
	if err == nil {
		t.Fatal("expected error for unauthenticated session, got nil")
	}
}

func TestHandleSyncPayload(t *testing.T) {
	tempDir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user":{"name":"Sync User","email":"syncuser@gmail.com"},"expires":"2026-10-12T00:00:00.000Z"}`))
	}))
	defer srv.Close()

	origEndpoint := FlowSessionEndpoint
	defer func() { FlowSessionEndpoint = origEndpoint }()
	FlowSessionEndpoint = srv.URL

	req := SyncRequest{
		SessionToken: "sync-token",
		CSRFToken:    "sync-csrf",
		ProfileDir:   "Profile 10",
	}

	cfg := &config.Config{AuthDir: tempDir}
	result, err := HandleSyncPayload(context.Background(), cfg, req)
	if err != nil {
		t.Fatalf("HandleSyncPayload failed: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK result, got: %#v", result)
	}
	if result.Email != "syncuser@gmail.com" {
		t.Fatalf("expected syncuser@gmail.com, got %s", result.Email)
	}
}

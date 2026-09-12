package auth

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/flow"
)

func TestBuildFlowAuthRecord(t *testing.T) {
	auth := &flow.FlowAuth{
		SessionToken: "token-12345",
		CSRFToken:    "csrf-67890",
		Email:        "user@gmail.com",
		Name:         "Flow User",
		ProfileDir:   "Profile 12",
		ExpiresAt:    time.Now().Add(48 * time.Hour).UnixMilli(),
	}

	rec := BuildFlowAuthRecord(auth)
	if rec == nil {
		t.Fatal("expected non-nil auth record")
	}
	if rec.Provider != "flow" {
		t.Fatalf("expected provider flow, got %s", rec.Provider)
	}
	if rec.FileName != "flow-user-gmail-com.json" {
		t.Fatalf("expected flow-user-gmail-com.json, got %s", rec.FileName)
	}
	if rec.Attributes["email"] != "user@gmail.com" {
		t.Fatalf("expected email attribute user@gmail.com, got %s", rec.Attributes["email"])
	}
	if rec.Attributes["profile_dir"] != "Profile 12" {
		t.Fatalf("expected profile_dir attribute Profile 12, got %s", rec.Attributes["profile_dir"])
	}
	if rec.Metadata["session_token"] != "token-12345" {
		t.Fatalf("expected session_token in metadata, got %v", rec.Metadata["session_token"])
	}
	if rec.Label != "Google Flow (Flow User)" {
		t.Fatalf("expected label Google Flow (Flow User), got %s", rec.Label)
	}
}

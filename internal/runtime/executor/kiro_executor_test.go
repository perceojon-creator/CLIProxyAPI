package executor

import (
	"net/http"
	"strings"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func TestKiroExecutorIdentifierAndFormat(t *testing.T) {
	e := NewKiroExecutor(nil)
	if e.Identifier() != "kiro" {
		t.Fatalf("Identifier() = %q, want kiro", e.Identifier())
	}
	for _, from := range []sdktranslator.Format{sdktranslator.FormatClaude, sdktranslator.FormatGemini, sdktranslator.FormatOpenAI} {
		got := e.RequestToFormat(cliproxyexecutor.Request{}, cliproxyexecutor.Options{SourceFormat: from})
		if got != sdktranslator.FormatOpenAI {
			t.Fatalf("RequestToFormat(%s) = %s, want openai", from, got)
		}
	}
}

func TestNormalizeKiroUpstreamModel(t *testing.T) {
	cases := map[string]string{
		"claude-sonnet-4.5":      "claude-sonnet-4.5",
		"kiro-claude-sonnet-4.5": "claude-sonnet-4.5",
		"KIRO-auto":              "auto",
		"auto(high)":             "auto",
		"":                       "auto",
		"kiro-":                  "kiro-",
		"  gpt-5.6-sol  ":        "gpt-5.6-sol",
	}
	for input, want := range cases {
		if got := normalizeKiroUpstreamModel(input); got != want {
			t.Fatalf("normalizeKiroUpstreamModel(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestKiroCredsPrefersMetadataThenAttributes(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Metadata: map[string]any{
			"access_token": "meta-token",
			"region":       "eu-west-1",
		},
		Attributes: map[string]string{
			"access_token":  "attr-token",
			"refresh_token": "attr-refresh",
			"profile_arn":   "arn:aws:codewhisperer:eu-west-1:1:profile/X",
		},
	}
	creds := kiroCreds(auth)
	if creds.accessToken != "meta-token" {
		t.Fatalf("accessToken = %q, want meta-token", creds.accessToken)
	}
	if creds.refreshToken != "attr-refresh" {
		t.Fatalf("refreshToken = %q, want attr-refresh", creds.refreshToken)
	}
	if creds.region != "eu-west-1" {
		t.Fatalf("region = %q, want eu-west-1", creds.region)
	}
	if creds.authMethod != "social" {
		t.Fatalf("authMethod = %q, want social (default)", creds.authMethod)
	}
}

func TestKiroCredsDefaultsRegionForNilAuth(t *testing.T) {
	creds := kiroCreds(nil)
	if creds.region != kiroDefaultRegion {
		t.Fatalf("region = %q, want %q", creds.region, kiroDefaultRegion)
	}
	if creds.accessToken != "" {
		t.Fatalf("accessToken = %q, want empty", creds.accessToken)
	}
}

func TestKiroRuntimeEndpointUsesRegion(t *testing.T) {
	if got, want := kiroRuntimeEndpoint("eu-west-1"), "https://runtime.eu-west-1.kiro.dev/generateAssistantResponse"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
	if got, want := kiroRuntimeEndpoint(" "), "https://runtime."+kiroDefaultRegion+".kiro.dev/generateAssistantResponse"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestApplyKiroHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://runtime.us-east-1.kiro.dev/generateAssistantResponse", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	applyKiroHeaders(req, &cliproxyauth.Auth{Metadata: map[string]any{"access_token": "tok"}})

	if got := req.Header.Get("Authorization"); got != "Bearer tok" {
		t.Fatalf("Authorization = %q, want Bearer tok", got)
	}
	if got := req.Header.Get("x-amzn-kiro-agent-mode"); got != kiroAgentMode {
		t.Fatalf("agent mode = %q, want %q", got, kiroAgentMode)
	}
	if got := req.Header.Get("Accept"); !strings.Contains(got, "application/vnd.amazon.eventstream") {
		t.Fatalf("Accept = %q, want the amazon eventstream media type", got)
	}
	if got := req.Header.Get("amz-sdk-invocation-id"); len(got) != 36 {
		t.Fatalf("amz-sdk-invocation-id = %q, want a UUID", got)
	}
	if got := req.Header.Get("x-amz-user-agent"); got != kiroClientVersion {
		t.Fatalf("x-amz-user-agent = %q, want %q", got, kiroClientVersion)
	}
}

func TestKiroConversationIDIsStablePerAuthAndModel(t *testing.T) {
	auth := &cliproxyauth.Auth{ID: "kiro-test-stable.json"}
	first := kiroConversationID(auth, "auto")
	if first != kiroConversationID(auth, "auto") {
		t.Fatal("conversation ID must be stable for the same auth and model")
	}
	if first == kiroConversationID(auth, "claude-opus-5") {
		t.Fatal("conversation ID must differ per model")
	}
	other := &cliproxyauth.Auth{ID: "kiro-test-other.json"}
	if first == kiroConversationID(other, "auto") {
		t.Fatal("conversation ID must differ per credential")
	}
}

func TestKiroStatusErrLabelsKnownFailures(t *testing.T) {
	cases := map[int]string{
		http.StatusPaymentRequired: "credit limit",
		http.StatusForbidden:       "sign in again",
		http.StatusTooManyRequests: "rate limit",
		http.StatusBadGateway:      "failed (502)",
	}
	for status, fragment := range cases {
		err := kiroStatusErr(status, []byte(`{"message":"boom"}`))
		statusCarrier, ok := err.(cliproxyexecutor.StatusError)
		if !ok {
			t.Fatalf("kiroStatusErr(%d) does not implement StatusError", status)
		}
		if statusCarrier.StatusCode() != status {
			t.Fatalf("StatusCode() = %d, want %d", statusCarrier.StatusCode(), status)
		}
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error %q does not contain %q", err.Error(), fragment)
		}
		if !strings.Contains(err.Error(), "boom") {
			t.Fatalf("error %q should carry the upstream detail", err.Error())
		}
	}
}

func TestKiroStatusErrTruncatesLongBodies(t *testing.T) {
	err := kiroStatusErr(http.StatusInternalServerError, []byte(strings.Repeat("x", 900)))
	if count := strings.Count(err.Error(), "x"); count != 400 {
		t.Fatalf("detail length = %d, want 400", count)
	}
}

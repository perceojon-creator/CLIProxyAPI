package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/flow"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func mockFlowAuth() *cliproxyauth.Auth {
	storage := &flow.FlowTokenStorage{
		SessionToken: "test-session-cookie",
		Cookies:      "test-session-cookie",
		AtToken:      "AIQ-test:12345",
		ProjectID:    "9dd588d0-405b-4fb9-95f7-063bd0ab8f33",
		Email:        "perceojon@gmail.com",
		Name:         "perceo jon",
		Type:         "flow",
	}
	return &cliproxyauth.Auth{
		ID:       "flow-perceojon@gmail.com.json",
		Provider: "flow",
		Label:    "Google Flow (perceo jon)",
		Storage:  storage,
		Metadata: map[string]any{
			"cookies":    "test-session-cookie",
			"at_token":   "AIQ-test:12345",
			"project_id": "9dd588d0-405b-4fb9-95f7-063bd0ab8f33",
			"email":      "perceojon@gmail.com",
		},
	}
}

func TestFlowExecutor_Basics(t *testing.T) {
	cfg := &config.Config{}
	ex := NewFlowExecutor(cfg)

	if id := ex.Identifier(); id != "flow" {
		t.Fatalf("expected flow, got %s", id)
	}

	fmt := ex.RequestToFormat(cliproxyexecutor.Request{}, cliproxyexecutor.Options{})
	if fmt != sdktranslator.FormatOpenAI {
		t.Fatalf("expected openai format, got %v", fmt)
	}
}

func TestFlowExecutor_CountTokens(t *testing.T) {
	cfg := &config.Config{}
	ex := NewFlowExecutor(cfg)

	payload := []byte(`{"messages":[{"role":"user","content":"neon lightning cyberpunk hawk"}]}`)
	req := cliproxyexecutor.Request{
		Model:   "flow-nano-banana-2",
		Payload: payload,
	}

	resp, err := ex.CountTokens(context.Background(), mockFlowAuth(), req, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("CountTokens failed: %v", err)
	}

	totalTokens := gjson.GetBytes(resp.Payload, "usage.total_tokens").Int()
	if totalTokens < 4 {
		t.Fatalf("expected at least 4 tokens, got %d", totalTokens)
	}
}

func TestFlowExecutor_ExecuteChatImage(t *testing.T) {
	cfg := &config.Config{}
	ex := NewFlowExecutor(cfg)

	payload := []byte(`{"messages":[{"role":"user","content":"neon cyberpunk skyline"}]}`)
	req := cliproxyexecutor.Request{
		Model:   "flow-nano-banana-2",
		Payload: payload,
	}
	opts := cliproxyexecutor.Options{}

	resp, err := ex.Execute(context.Background(), mockFlowAuth(), req, opts)
	if err != nil {
		t.Fatalf("Execute chat image failed: %v", err)
	}

	content := gjson.GetBytes(resp.Payload, "choices.0.message.content").String()
	if !strings.Contains(content, "Generated Image") {
		t.Fatalf("expected markdown image, got: %s", content)
	}
}

func TestFlowExecutor_ExecuteChatVideo(t *testing.T) {
	cfg := &config.Config{}
	ex := NewFlowExecutor(cfg)

	payload := []byte(`{"messages":[{"role":"user","content":"flying eagle in 4k neon city"}]}`)
	req := cliproxyexecutor.Request{
		Model:   "flow-veo-3.1",
		Payload: payload,
	}
	opts := cliproxyexecutor.Options{}

	resp, err := ex.Execute(context.Background(), mockFlowAuth(), req, opts)
	if err != nil {
		t.Fatalf("Execute chat video failed: %v", err)
	}

	content := gjson.GetBytes(resp.Payload, "choices.0.message.content").String()
	if !strings.Contains(content, "generated video") {
		t.Fatalf("expected video markdown, got: %s", content)
	}
}

func TestFlowExecutor_ExecuteImagesGenerations(t *testing.T) {
	cfg := &config.Config{}
	ex := NewFlowExecutor(cfg)

	payload := []byte(`{"prompt":"cyberpunk eagle","model":"flow-nano-banana-2","response_format":"url"}`)
	req := cliproxyexecutor.Request{
		Model:   "flow-nano-banana-2",
		Payload: payload,
	}
	opts := cliproxyexecutor.Options{
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/images/generations",
		},
	}

	resp, err := ex.Execute(context.Background(), mockFlowAuth(), req, opts)
	if err != nil {
		t.Fatalf("Execute images generations failed: %v", err)
	}

	var parsed map[string]any
	if errJSON := json.Unmarshal(resp.Payload, &parsed); errJSON != nil {
		t.Fatalf("expected valid json response: %v", errJSON)
	}

	dataArr, ok := parsed["data"].([]any)
	if !ok || len(dataArr) == 0 {
		t.Fatalf("expected data array in response, got: %v", parsed)
	}
}

func TestFlowExecutor_ExecuteVideosGenerations(t *testing.T) {
	cfg := &config.Config{}
	ex := NewFlowExecutor(cfg)

	payload := []byte(`{"prompt":"majestic eagle flying","model":"flow-veo-3.1"}`)
	req := cliproxyexecutor.Request{
		Model:   "flow-veo-3.1",
		Payload: payload,
	}
	opts := cliproxyexecutor.Options{
		Metadata: map[string]any{
			cliproxyexecutor.RequestPathMetadataKey: "/v1/videos/generations",
		},
	}

	resp, err := ex.Execute(context.Background(), mockFlowAuth(), req, opts)
	if err != nil {
		t.Fatalf("Execute videos generations failed: %v", err)
	}

	status := gjson.GetBytes(resp.Payload, "status").String()
	if status != "completed" {
		t.Fatalf("expected status completed, got: %s", status)
	}
}

func TestFlowExecutor_ExecuteStream(t *testing.T) {
	cfg := &config.Config{}
	ex := NewFlowExecutor(cfg)

	payload := []byte(`{"messages":[{"role":"user","content":"stream prompt test"}]}`)
	req := cliproxyexecutor.Request{
		Model:   "flow-nano-banana-2",
		Payload: payload,
	}

	res, err := ex.ExecuteStream(context.Background(), mockFlowAuth(), req, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("ExecuteStream failed: %v", err)
	}

	var chunks []string
	for chunk := range res.Chunks {
		chunks = append(chunks, string(chunk.Payload))
	}

	if len(chunks) == 0 {
		t.Fatalf("expected streaming chunks, got none")
	}

	full := strings.Join(chunks, "")
	if !strings.Contains(full, "[DONE]") {
		t.Fatalf("expected [DONE] chunk, got: %s", full)
	}
}

func TestFlowExecutor_HttpRequest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	cfg := &config.Config{}
	ex := NewFlowExecutor(cfg)

	req, _ := http.NewRequest(http.MethodGet, ts.URL, nil)
	resp, err := ex.HttpRequest(context.Background(), mockFlowAuth(), req)
	if err != nil {
		t.Fatalf("HttpRequest failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

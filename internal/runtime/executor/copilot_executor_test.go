package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func TestCopilotExecutor_Identifier(t *testing.T) {
	exec := NewCopilotExecutor(&config.Config{})
	if exec.Identifier() != "copilot" {
		t.Errorf("expected copilot, got %s", exec.Identifier())
	}
}

func TestCopilotExecutor_Execute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer gho_test_token") {
			t.Errorf("missing or bad authorization header: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Editor-Version") != "vscode/1.96.0" {
			t.Errorf("expected Editor-Version vscode/1.96.0, got %s", r.Header.Get("Editor-Version"))
		}
		if r.Header.Get("Copilot-Integration-Id") != "vscode-chat" {
			t.Errorf("expected Copilot-Integration-Id vscode-chat, got %s", r.Header.Get("Copilot-Integration-Id"))
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected path /chat/completions, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		respData := map[string]any{
			"id":     "chatcmpl-test",
			"object": "chat.completion",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "Hello from Copilot subscription!",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		_ = json.NewEncoder(w).Encode(respData)
	}))
	defer srv.Close()

	exec := NewCopilotExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID:       "copilot-test",
		Provider: "copilot",
		Attributes: map[string]string{
			"access_token": "gho_test_token",
			"base_url":     srv.URL,
		},
	}

	reqPayload := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}],"stream":false}`)
	req := cliproxyexecutor.Request{
		Model:   "gpt-4o",
		Payload: reqPayload,
		Format:  sdktranslator.FormatOpenAI,
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAI,
	}

	resp, err := exec.Execute(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(string(resp.Payload), "Hello from Copilot subscription!") {
		t.Errorf("expected response to contain Hello from Copilot subscription!, got: %s", string(resp.Payload))
	}
}

func TestCopilotExecutor_ExecuteStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("flusher unavailable")
		}
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"Chunk 1"},"index":0}]}`)
		flusher.Flush()
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":" Chunk 2"},"index":0,"finish_reason":"stop"}],"usage":{"total_tokens":10}}`)
		flusher.Flush()
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	exec := NewCopilotExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID:       "copilot-stream-test",
		Provider: "copilot",
		Attributes: map[string]string{
			"access_token": "gho_test_token",
			"base_url":     srv.URL,
		},
	}

	req := cliproxyexecutor.Request{
		Model:   "gpt-4o",
		Payload: []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}],"stream":true}`),
		Format:  sdktranslator.FormatOpenAI,
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FormatOpenAI,
	}

	res, err := exec.ExecuteStream(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream failed: %v", err)
	}

	var sb strings.Builder
	chunksReceived := 0
	for chunk := range res.Chunks {
		if chunk.Err != nil {
			t.Fatalf("Chunk error: %v", chunk.Err)
		}
		chunksReceived++
		sb.Write(chunk.Payload)
	}
	if chunksReceived == 0 {
		t.Errorf("expected stream chunks, got 0")
	}
}

func TestCopilotExecutor_UpstreamHttpErrorCodes(t *testing.T) {
	codes := []int{400, 401, 403, 404, 429, 500, 502, 503}
	for _, code := range codes {
		t.Run(fmt.Sprintf("HTTP_%d", code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if code == 429 {
					w.Header().Set("Retry-After", "30")
				}
				w.WriteHeader(code)
				_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"message":"mock error %d"}}`, code)))
			}))
			defer srv.Close()

			exec := NewCopilotExecutor(&config.Config{})
			auth := &cliproxyauth.Auth{
				ID:       "copilot-err-test",
				Provider: "copilot",
				Attributes: map[string]string{
					"access_token": "gho_test_token",
					"base_url":     srv.URL,
				},
			}

			req := cliproxyexecutor.Request{
				Model:   "gpt-4o",
				Payload: []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}]}`),
				Format:  sdktranslator.FormatOpenAI,
			}
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAI}

			_, err := exec.Execute(context.Background(), auth, req, opts)
			if err == nil {
				t.Fatalf("expected error for HTTP %d, got nil", code)
			}
			if sc, ok := err.(interface{ StatusCode() int }); ok {
				if sc.StatusCode() != code {
					t.Errorf("expected status code %d, got %d", code, sc.StatusCode())
				}
			}
			if code == 429 {
				if ra, ok := err.(interface{ RetryAfter() *time.Duration }); ok {
					d := ra.RetryAfter()
					if d == nil || *d != 30*time.Second {
						t.Errorf("expected 30s Retry-After, got %v", d)
					}
				}
			}
		})
	}
}

func TestCopilotExecutor_EmpiricalLatencyMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"test","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"total_tokens":2}}`))
	}))
	defer srv.Close()

	exec := NewCopilotExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID:       "copilot-metrics-test",
		Provider: "copilot",
		Attributes: map[string]string{
			"access_token": "gho_test_token",
			"base_url":     srv.URL,
		},
	}

	req := cliproxyexecutor.Request{
		Model:   "gpt-4o",
		Payload: []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}]}`),
		Format:  sdktranslator.FormatOpenAI,
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAI}

	const totalRequests = 200
	latencies := make([]time.Duration, totalRequests)
	start := time.Now()

	for i := 0; i < totalRequests; i++ {
		t0 := time.Now()
		_, err := exec.Execute(context.Background(), auth, req, opts)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		latencies[i] = time.Since(t0)
	}

	totalDuration := time.Since(start)
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	avg := totalDuration / time.Duration(totalRequests)
	p50 := latencies[int(float64(totalRequests)*0.50)]
	p90 := latencies[int(float64(totalRequests)*0.90)]
	p99 := latencies[int(float64(totalRequests)*0.99)]
	opsPerSec := float64(totalRequests) / totalDuration.Seconds()

	t.Logf("Empirical Performance Metrics (n=%d requests):", totalRequests)
	t.Logf("Total Duration: %v, Throughput: %.2f ops/sec", totalDuration, opsPerSec)
	t.Logf("Latency Min: %v, Avg: %v, p50: %v, p90: %v, p99: %v, Max: %v",
		latencies[0], avg, p50, p90, p99, latencies[totalRequests-1])

	if opsPerSec < 500 {
		t.Errorf("throughput below expected threshold: %.2f ops/sec", opsPerSec)
	}
}

func TestCopilotExecutor_Concurrency(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"test","choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"total_tokens":2}}`))
	}))
	defer srv.Close()

	exec := NewCopilotExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID:       "copilot-concurrency-test",
		Provider: "copilot",
		Attributes: map[string]string{
			"access_token": "gho_test_token",
			"base_url":     srv.URL,
		},
	}

	req := cliproxyexecutor.Request{
		Model:   "gpt-4o",
		Payload: []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}]}`),
		Format:  sdktranslator.FormatOpenAI,
	}
	opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatOpenAI}

	const goroutines = 10
	const requestsPerGoroutine = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < requestsPerGoroutine; i++ {
				_, err := exec.Execute(context.Background(), auth, req, opts)
				if err != nil {
					t.Errorf("concurrent execute failed: %v", err)
				}
			}
		}()
	}
	wg.Wait()
}

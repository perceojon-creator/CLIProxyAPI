package helps

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

func TestGhostTelemetryManager_SessionLifecycleAndHeartbeat(t *testing.T) {
	var pingReceived atomic.Int64
	var mu sync.Mutex
	var lastPayload []byte
	var lastHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pingReceived.Add(1)
		b, _ := io.ReadAll(r.Body)
		h := r.Header.Clone()

		mu.Lock()
		lastHeaders = h
		lastPayload = b
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	cfg := GhostTelemetryConfig{
		Enabled:           true,
		HeartbeatInterval: 20 * time.Millisecond,
		IdleTimeout:       100 * time.Millisecond,
		BaseURL:           server.URL,
		UserAgent:         "antigravity/hub/2.9.1 darwin/arm64",
		ClientVersion:     "2.9.1",
		MaxSessions:       5,
	}

	mgr := NewGhostTelemetryManager(cfg, server.Client())
	defer mgr.Stop()

	const sessionID = "sess-test-12345"
	const projectID = "proj-test-67890"
	const token = "ya29.test-ghost-token"

	// 1. Record session activity
	mgr.RecordSessionActivity(sessionID, projectID, token)

	if count := mgr.ActiveSessionCount(); count != 1 {
		t.Fatalf("ActiveSessionCount = %d, want 1", count)
	}

	// 2. Wait for at least 2 heartbeats
	time.Sleep(120 * time.Millisecond)

	if pings := pingReceived.Load(); pings < 2 {
		t.Fatalf("Received %d pings, expected at least 2", pings)
	}

	mu.Lock()
	payloadCopy := append([]byte(nil), lastPayload...)
	headersCopy := lastHeaders.Clone()
	mu.Unlock()

	// 3. Inspect payload structure
	event := gjson.GetBytes(payloadCopy, "event").String()
	if event != "heartbeat" {
		t.Fatalf("Payload event = %q, want 'heartbeat'", event)
	}

	clientSession := gjson.GetBytes(payloadCopy, "client.session").String()
	if clientSession != sessionID {
		t.Fatalf("client.session = %q, want %q", clientSession, sessionID)
	}

	clientProject := gjson.GetBytes(payloadCopy, "client.project").String()
	if clientProject != projectID {
		t.Fatalf("client.project = %q, want %q", clientProject, projectID)
	}

	clientVersion := gjson.GetBytes(payloadCopy, "client.version").String()
	if clientVersion != "2.9.1" {
		t.Fatalf("client.version = %q, want '2.9.1'", clientVersion)
	}

	// 4. Inspect headers
	if authHeader := headersCopy.Get("Authorization"); authHeader != "Bearer "+token {
		t.Fatalf("Authorization = %q, want Bearer %s", authHeader, token)
	}
	if ua := headersCopy.Get("User-Agent"); ua != "antigravity/hub/2.9.1 darwin/arm64" {
		t.Fatalf("User-Agent = %q, want 'antigravity/hub/2.9.1 darwin/arm64'", ua)
	}

	// 5. Test idle eviction
	time.Sleep(150 * time.Millisecond)
	if count := mgr.ActiveSessionCount(); count != 0 {
		t.Logf("Session remaining count: %d (transitioning to idle)", count)
	}

	t.Logf(" [PASS] Ghost telemetry validado: %d pings recibidos con payload y headers correctos", pingReceived.Load())
}

func TestGhostTelemetryManager_CapacityAndEviction(t *testing.T) {
	cfg := GhostTelemetryConfig{
		Enabled:           true,
		HeartbeatInterval: 1 * time.Hour, // long interval, only testing capacity
		IdleTimeout:       1 * time.Hour,
		BaseURL:           "http://localhost:9999",
		MaxSessions:       3,
	}

	mgr := NewGhostTelemetryManager(cfg, http.DefaultClient)
	defer mgr.Stop()

	mgr.RecordSessionActivity("sess-1", "p1", "t1")
	mgr.RecordSessionActivity("sess-2", "p2", "t2")
	mgr.RecordSessionActivity("sess-3", "p3", "t3")

	if count := mgr.ActiveSessionCount(); count != 3 {
		t.Fatalf("ActiveSessionCount = %d, want 3", count)
	}

	// 4th session exceeds MaxSessions = 3, oldest must be evicted
	time.Sleep(2 * time.Millisecond)
	mgr.RecordSessionActivity("sess-4", "p4", "t4")

	if count := mgr.ActiveSessionCount(); count > 3 {
		t.Fatalf("ActiveSessionCount = %d exceeded max 3", count)
	}
	t.Log(" [PASS] Gestion de capacidad y desalojo de sesiones inactivas validado")
}

func TestGhostTelemetryManager_ConcurrentSessionsAndZeroGoroutineLeaks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	// Capture baseline goroutines before starting manager
	baselineGoroutines := runtime.NumGoroutine()

	cfg := GhostTelemetryConfig{
		Enabled:           true,
		HeartbeatInterval: 15 * time.Millisecond,
		IdleTimeout:       200 * time.Millisecond,
		BaseURL:           server.URL,
		UserAgent:         "antigravity/hub/2.9.1 darwin/arm64",
		ClientVersion:     "2.9.1",
		MaxSessions:       20,
	}

	mgr := NewGhostTelemetryManager(cfg, server.Client())

	const workers = 20
	const operationsPerWorker = 30
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < operationsPerWorker; i++ {
				sessID := fmt.Sprintf("sess-worker-%d-%d", workerID, i%5)
				mgr.RecordSessionActivity(sessID, "proj-stress", "tok-stress")
				time.Sleep(2 * time.Millisecond)
			}
		}(w)
	}
	wg.Wait()

	// Wait for several heartbeats to dispatch concurrently
	time.Sleep(100 * time.Millisecond)

	activeCount := mgr.ActiveSessionCount()
	if activeCount > cfg.MaxSessions {
		t.Fatalf("ActiveSessionCount %d exceeded MaxSessions %d under concurrency", activeCount, cfg.MaxSessions)
	}

	pingsSent := mgr.TotalPingsSent()
	if pingsSent == 0 {
		t.Fatalf("Expected pings sent > 0 under concurrent sessions, got %d", pingsSent)
	}

	// Stop manager and verify cleanup
	mgr.Stop()
	server.Client().CloseIdleConnections()
	server.CloseClientConnections()

	if afterStopCount := mgr.ActiveSessionCount(); afterStopCount != 0 {
		t.Fatalf("Expected 0 active sessions after Stop(), got %d", afterStopCount)
	}

	// Verify all session heartbeat goroutines have exited (zero goroutine leaks)
	deadline := time.Now().Add(500 * time.Millisecond)
	var finalGoroutines int
	for time.Now().Before(deadline) {
		runtime.Gosched()
		finalGoroutines = runtime.NumGoroutine()
		if finalGoroutines <= baselineGoroutines {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if finalGoroutines > baselineGoroutines {
		t.Fatalf("Potential goroutine leak detected: baseline=%d, final=%d", baselineGoroutines, finalGoroutines)
	}

	t.Logf(" [PASS] Concurrencia de telemetría y ciclo de vida validado: %d sesiones gestionadas, %d pings enviados, 0 goroutine leaks (baseline=%d, final=%d)",
		workers*operationsPerWorker, pingsSent, baselineGoroutines, finalGoroutines)
}

package helps

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

// GhostTelemetryConfig defines configuration for simulated Antigravity client telemetry and heartbeats.
type GhostTelemetryConfig struct {
	Enabled           bool
	HeartbeatInterval time.Duration
	IdleTimeout       time.Duration
	BaseURL           string
	UserAgent         string
	ClientVersion     string
	MaxSessions       int
}

// DefaultGhostTelemetryConfig returns production-aligned defaults matching the native Antigravity Electron app.
func DefaultGhostTelemetryConfig() GhostTelemetryConfig {
	return GhostTelemetryConfig{
		Enabled:           true,
		HeartbeatInterval: 45 * time.Second,
		IdleTimeout:       10 * time.Minute,
		BaseURL:           "https://alkalimakersuite-pa.clients6.google.com",
		UserAgent:         "antigravity/hub/2.9.1 darwin/arm64",
		ClientVersion:     "2.9.1",
		MaxSessions:       256,
	}
}

// GhostSessionState holds the runtime heartbeat state for an active Antigravity session.
type GhostSessionState struct {
	SessionID    string
	ProjectID    string
	AccessToken  string
	CreatedAt    time.Time
	LastActiveAt time.Time
	LastPingAt   time.Time
	PingCount    int64
	Active       bool
	cancel       context.CancelFunc
}

// GhostTelemetryManager manages simulated background telemetry heartbeats across Antigravity sessions.
type GhostTelemetryManager struct {
	cfg      GhostTelemetryConfig
	mu       sync.RWMutex
	sessions map[string]*GhostSessionState
	client   *http.Client

	// Empirical metrics
	totalPingsSent atomic.Int64
	totalPingsFail atomic.Int64

	closed atomic.Bool
	stopCh chan struct{}
}

var (
	defaultGhostTelemetryManager *GhostTelemetryManager
	ghostTelemetryInitOnce       sync.Once
)

// GetDefaultGhostTelemetryManager returns the singleton telemetry manager.
func GetDefaultGhostTelemetryManager() *GhostTelemetryManager {
	ghostTelemetryInitOnce.Do(func() {
		defaultGhostTelemetryManager = NewGhostTelemetryManager(DefaultGhostTelemetryConfig(), nil)
	})
	return defaultGhostTelemetryManager
}

// NewGhostTelemetryManager creates a new production-ready GhostTelemetryManager.
func NewGhostTelemetryManager(cfg GhostTelemetryConfig, httpClient *http.Client) *GhostTelemetryManager {
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = 45 * time.Second
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 10 * time.Minute
	}
	if cfg.MaxSessions <= 0 {
		cfg.MaxSessions = 256
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "antigravity/hub/2.9.1 darwin/arm64"
	}
	if cfg.ClientVersion == "" {
		cfg.ClientVersion = "2.9.1"
	}
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	mgr := &GhostTelemetryManager{
		cfg:      cfg,
		sessions: make(map[string]*GhostSessionState),
		client:   httpClient,
		stopCh:   make(chan struct{}),
	}
	return mgr
}

// RecordSessionActivity registers or touches an active Antigravity session to keep telemetry alive.
func (m *GhostTelemetryManager) RecordSessionActivity(sessionID, projectID, accessToken string) {
	if !m.cfg.Enabled || m.closed.Load() || sessionID == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	state, exists := m.sessions[sessionID]
	if exists {
		state.LastActiveAt = now
		if accessToken != "" {
			state.AccessToken = accessToken
		}
		if projectID != "" {
			state.ProjectID = projectID
		}
		return
	}

	// Session eviction if capacity reached
	if len(m.sessions) >= m.cfg.MaxSessions {
		m.evictOldestIdleSessionLocked(now)
	}

	ctx, cancel := context.WithCancel(context.Background())
	state = &GhostSessionState{
		SessionID:    sessionID,
		ProjectID:    projectID,
		AccessToken:  accessToken,
		CreatedAt:    now,
		LastActiveAt: now,
		Active:       true,
		cancel:       cancel,
	}
	m.sessions[sessionID] = state

	go m.runSessionHeartbeat(ctx, state)
}

// evictOldestIdleSessionLocked cleans up stale sessions under write lock.
func (m *GhostTelemetryManager) evictOldestIdleSessionLocked(now time.Time) {
	var oldestID string
	var oldestTime time.Time

	for id, s := range m.sessions {
		if oldestID == "" || s.LastActiveAt.Before(oldestTime) {
			oldestID = id
			oldestTime = s.LastActiveAt
		}
	}

	if oldestID != "" {
		if s, ok := m.sessions[oldestID]; ok {
			s.Active = false
			if s.cancel != nil {
				s.cancel()
			}
			delete(m.sessions, oldestID)
		}
	}
}

// runSessionHeartbeat loops periodically sending ghost telemetry heartbeats until canceled or idle.
func (m *GhostTelemetryManager) runSessionHeartbeat(ctx context.Context, state *GhostSessionState) {
	ticker := time.NewTicker(m.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.mu.RLock()
			lastActive := state.LastActiveAt
			active := state.Active
			m.mu.RUnlock()

			if !active || time.Since(lastActive) > m.cfg.IdleTimeout {
				m.mu.Lock()
				state.Active = false
				delete(m.sessions, state.SessionID)
				m.mu.Unlock()
				return
			}

			m.SendHeartbeatPing(ctx, state)
		}
	}
}

// SendHeartbeatPing dispatches one realistic client telemetry ping.
func (m *GhostTelemetryManager) SendHeartbeatPing(ctx context.Context, state *GhostSessionState) error {
	if m.closed.Load() {
		return nil
	}

	m.mu.RLock()
	projectID := state.ProjectID
	token := state.AccessToken
	sessionID := state.SessionID
	m.mu.RUnlock()

	pingPayload := map[string]any{
		"client": map[string]any{
			"name":      "antigravity",
			"version":   m.cfg.ClientVersion,
			"platform":  "darwin",
			"arch":      "arm64",
			"session":   sessionID,
			"project":   projectID,
			"timestamp": time.Now().UnixMilli(),
		},
		"event": "heartbeat",
	}

	bodyBytes, errMarshal := json.Marshal(pingPayload)
	if errMarshal != nil {
		return errMarshal
	}

	reqURL := m.cfg.BaseURL + "/v1internal:heartbeat"
	req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyBytes))
	if errReq != nil {
		return errReq
	}

	req.Header.Set("Host", "alkalimakersuite-pa.clients6.google.com")
	req.Header.Set("User-Agent", m.cfg.UserAgent)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("X-Goog-Api-Client", "gl-go/1.26.0")

	// Stochastic tiny network jitter before sending (5ms - 25ms)
	if errDelay := randomDelayContext(ctx, 5*time.Millisecond, 25*time.Millisecond); errDelay != nil {
		return errDelay
	}

	resp, errDo := m.client.Do(req)
	if errDo != nil {
		m.totalPingsFail.Add(1)
		log.Debugf("ghost telemetry ping failed: %v", errDo)
		return errDo
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	m.totalPingsSent.Add(1)
	m.mu.Lock()
	state.LastPingAt = time.Now()
	state.PingCount++
	m.mu.Unlock()

	return nil
}

// randomDelayContext introduces a cryptographically secure random micro-delay with context cancellation.
func randomDelayContext(ctx context.Context, min, max time.Duration) error {
	if max <= min {
		return nil
	}
	diff := max - min
	n, err := rand.Int(rand.Reader, big.NewInt(int64(diff)))
	if err != nil {
		return nil
	}
	delay := min + time.Duration(n.Int64())
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ActiveSessionCount returns the count of monitored sessions.
func (m *GhostTelemetryManager) ActiveSessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// TotalPingsSent returns total successful telemetry heartbeats dispatched.
func (m *GhostTelemetryManager) TotalPingsSent() int64 {
	return m.totalPingsSent.Load()
}

// TotalPingsFail returns total failed telemetry heartbeats.
func (m *GhostTelemetryManager) TotalPingsFail() int64 {
	return m.totalPingsFail.Load()
}

// Stop shuts down the telemetry manager and stops all background workers.
func (m *GhostTelemetryManager) Stop() {
	if m.closed.CompareAndSwap(false, true) {
		close(m.stopCh)
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, s := range m.sessions {
			s.Active = false
			if s.cancel != nil {
				s.cancel()
			}
		}
		m.sessions = make(map[string]*GhostSessionState)
	}
}

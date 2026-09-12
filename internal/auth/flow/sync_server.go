package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	log "github.com/sirupsen/logrus"
)

// SyncRequest represents the JSON payload posted to the local Flow sync bridge.
type SyncRequest struct {
	SessionToken string `json:"session_token"`
	CSRFToken    string `json:"csrf_token,omitempty"`
	Email        string `json:"email,omitempty"`
	Name         string `json:"name,omitempty"`
	ProfileDir   string `json:"profile_dir,omitempty"`
}

// SyncResult reports the status of an ingested Flow credential.
type SyncResult struct {
	OK      bool   `json:"ok"`
	Email   string `json:"email,omitempty"`
	Name    string `json:"name,omitempty"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message,omitempty"`
}

// HandleSyncPayload validates and saves a Flow session token into the auth directory.
func HandleSyncPayload(ctx context.Context, cfg *config.Config, req SyncRequest) (*SyncResult, error) {
	token := strings.TrimSpace(req.SessionToken)
	if token == "" {
		return &SyncResult{OK: false, Message: "session_token is required"}, fmt.Errorf("session_token is empty")
	}

	// Verify session against labs.google
	userInfo, errVerify := VerifyFlowSession(ctx, token, cfg)
	if errVerify != nil {
		return &SyncResult{OK: false, Message: errVerify.Error()}, fmt.Errorf("session verification failed: %w", errVerify)
	}

	email := strings.TrimSpace(userInfo.Email)
	if email == "" {
		email = strings.TrimSpace(req.Email)
	}
	if email == "" {
		return &SyncResult{OK: false, Message: "could not determine account email"}, fmt.Errorf("missing email in session")
	}

	name := strings.TrimSpace(userInfo.Name)
	if name == "" {
		name = strings.TrimSpace(req.Name)
	}

	var expiresAt int64
	if userInfo.Expires != "" {
		if t, err := time.Parse(time.RFC3339, userInfo.Expires); err == nil {
			expiresAt = t.UnixMilli()
		}
	}

	auth := &FlowAuth{
		SessionToken: token,
		CSRFToken:    strings.TrimSpace(req.CSRFToken),
		Email:        email,
		Name:         name,
		ProfileDir:   strings.TrimSpace(req.ProfileDir),
		ExpiresAt:    expiresAt,
		UpdatedAt:    time.Now().UnixMilli(),
	}

	// Determine target save path
	authDir := "auths"
	if cfg != nil && strings.TrimSpace(cfg.AuthDir) != "" {
		authDir = cfg.AuthDir
	}
	fileName := fmt.Sprintf("flow-%s.json", email)
	savePath := filepath.Join(authDir, fileName)

	storage := NewTokenStorage(auth)
	if errSave := storage.SaveTokenToFile(savePath); errSave != nil {
		return &SyncResult{OK: false, Message: errSave.Error()}, fmt.Errorf("saving auth file: %w", errSave)
	}

	log.Infof("flow: session credentials successfully saved for %s (%s)", email, savePath)
	return &SyncResult{
		OK:      true,
		Email:   email,
		Name:    name,
		Path:    savePath,
		Message: "Session saved successfully",
	}, nil
}

// SyncServer is a lightweight loopback HTTP server that receives credentials from browsers or extensions.
type SyncServer struct {
	server *http.Server
	cfg    *config.Config
	mu     sync.Mutex
}

// NewSyncServer creates a new local sync server.
func NewSyncServer(cfg *config.Config, port int) *SyncServer {
	if port <= 0 {
		port = 51122
	}
	s := &SyncServer{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/flow/sync", s.handleSync)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	s.server = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: mux,
	}
	return s
}

func (s *SyncServer) handleSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	result, err := HandleSyncPayload(r.Context(), s.cfg, req)
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

// Start begins listening in background.
func (s *SyncServer) Start() error {
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("flow sync server error: %v", err)
		}
	}()
	return nil
}

// Stop gracefully stops the server.
func (s *SyncServer) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

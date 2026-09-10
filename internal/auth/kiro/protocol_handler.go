package kiro

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// KiroRedirectURI is the official redirect URI accepted by Kiro's AWS Cognito user pool.
	KiroRedirectURI = "kiro://kiro.kiroAgent/authenticate-success"
	// DefaultHandlerPort is the port used for local callback interception.
	DefaultHandlerPort = 19876
)

// AuthCallback holds the parsed parameters from an OAuth callback.
type AuthCallback struct {
	Code  string
	State string
	Error string
}

// ProtocolHandler listens on localhost and handles redirects from the kiro:// scheme.
type ProtocolHandler struct {
	port       int
	server     *http.Server
	listener   net.Listener
	resultChan chan *AuthCallback
	origCmd    string
	mu         sync.Mutex
	running    bool
}

// NewProtocolHandler creates a new protocol handler.
func NewProtocolHandler() *ProtocolHandler {
	return &ProtocolHandler{
		port:       DefaultHandlerPort,
		resultChan: make(chan *AuthCallback, 1),
	}
}

// Start spins up the HTTP callback listener and registers the Windows protocol interception.
func (h *ProtocolHandler) Start(ctx context.Context) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.running {
		return h.port, nil
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", h.port))
	if err != nil {
		for p := h.port + 1; p <= h.port+5; p++ {
			listener, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
			if err == nil {
				h.port = p
				break
			}
		}
	}
	if listener == nil {
		return 0, fmt.Errorf("kiro protocol handler: could not bind port: %w", err)
	}
	h.listener = listener

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/callback", h.handleHTTPCallback)
	h.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		if errServe := h.server.Serve(listener); errServe != nil && errServe != http.ErrServerClosed {
			log.Debugf("kiro protocol handler serve error: %v", errServe)
		}
	}()

	if runtime.GOOS == "windows" {
		h.origCmd = getWindowsOriginalKiroCommand()
		_ = installWindowsProtocolScripts(h.port)
	}

	h.running = true
	return h.port, nil
}

// Stop shuts down the listener and restores the original system protocol handler.
func (h *ProtocolHandler) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.running {
		return
	}
	h.running = false

	if h.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = h.server.Shutdown(ctx)
	}

	if runtime.GOOS == "windows" {
		restoreWindowsOriginalKiroCommand(h.origCmd)
	}
}

// ResultChan returns the channel where callback results are posted.
func (h *ProtocolHandler) ResultChan() <-chan *AuthCallback {
	return h.resultChan
}

// PostManualURL parses a raw kiro:// URL and delivers it to the callback channel.
func (h *ProtocolHandler) PostManualURL(rawURL string) bool {
	code, state, errParam := ParseKiroCallbackURL(rawURL)
	if code == "" && errParam == "" {
		return false
	}
	select {
	case h.resultChan <- &AuthCallback{Code: code, State: state, Error: errParam}:
		return true
	default:
		return false
	}
}

func (h *ProtocolHandler) handleHTTPCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	errParam := r.URL.Query().Get("error")

	select {
	case h.resultChan <- &AuthCallback{Code: code, State: state, Error: errParam}:
	default:
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errParam != "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "<html><body><h2>Login Failed</h2><p>%s</p></body></html>", html.EscapeString(errParam))
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>Kiro Login</title><style>body{font-family:sans-serif;background:#0f172a;color:#f8fafc;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;}.card{background:#1e293b;padding:2.5rem;border-radius:12px;text-align:center;}h2{color:#38bdf8;}</style></head><body><div class=\"card\"><h2>Autenticacion Exitosa!</h2><p>Tu cuenta de Google fue vinculada con Kiro.<br>Ya puedes regresar a la terminal.</p></div></body></html>")
}

// ParseKiroCallbackURL extracts code, state, and error from a kiro:// URL.
func ParseKiroCallbackURL(rawURL string) (string, string, string) {
	rawURL = strings.TrimSpace(rawURL)
	if !strings.HasPrefix(rawURL, "kiro://") && strings.Contains(rawURL, "code=") {
		rawURL = "kiro://kiro.kiroAgent/authenticate-success?" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", ""
	}
	q := u.Query()
	return q.Get("code"), q.Get("state"), q.Get("error")
}

func getWindowsOriginalKiroCommand() string {
	out, err := exec.Command("reg", "query", `HKCU\Software\Classes\kiro\shell\open\command`, "/ve").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	for _, l := range lines {
		if strings.Contains(l, "REG_SZ") {
			parts := strings.SplitN(l, "REG_SZ", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func installWindowsProtocolScripts(port int) error {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".cliproxyapi")
	_ = os.MkdirAll(dir, 0755)
	ps1Path := filepath.Join(dir, "kiro-oauth-handler.ps1")

	ps1 := fmt.Sprintf(`param([string]$url)
Add-Type -AssemblyName System.Web
$uri = [System.Uri]$url
$query = [System.Web.HttpUtility]::ParseQueryString($uri.Query)
$code = $query["code"]
$state = $query["state"]
$errorParam = $query["error"]
$callbackUrl = "http://127.0.0.1:%d/oauth/callback"
try {
  if ($errorParam) {
    Invoke-WebRequest -Uri ($callbackUrl + "?error=" + $errorParam) -UseBasicParsing -TimeoutSec 2 | Out-Null
  } elseif ($code -and $state) {
    Invoke-WebRequest -Uri ($callbackUrl + "?code=" + $code + "&state=" + $state) -UseBasicParsing -TimeoutSec 2 | Out-Null
  }
} catch {}
`, port)
	_ = os.WriteFile(ps1Path, []byte(ps1), 0644)

	cmdVal := fmt.Sprintf("powershell.exe -WindowStyle Hidden -ExecutionPolicy Bypass -File \"%s\" \"%%1\"", ps1Path)
	_ = exec.Command("reg", "add", `HKCU\Software\Classes\kiro`, "/ve", "/d", "URL:Kiro Protocol", "/f").Run()
	_ = exec.Command("reg", "add", `HKCU\Software\Classes\kiro`, "/v", "URL Protocol", "/d", "", "/f").Run()
	_ = exec.Command("reg", "add", `HKCU\Software\Classes\kiro\shell\open\command`, "/ve", "/d", cmdVal, "/f").Run()
	return nil
}

func restoreWindowsOriginalKiroCommand(orig string) {
	if strings.TrimSpace(orig) != "" {
		_ = exec.Command("reg", "add", `HKCU\Software\Classes\kiro\shell\open\command`, "/ve", "/d", orig, "/f").Run()
	}
}

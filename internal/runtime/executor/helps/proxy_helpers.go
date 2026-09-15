package helps

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
)

const (
	// DefaultHighConcurrencyMaxIdleConns defines the aggregate idle connections pool size (512).
	DefaultHighConcurrencyMaxIdleConns = 512

	// DefaultHighConcurrencyMaxIdleConnsPerHost defines the maximum idle keep-alive connections per host (64).
	DefaultHighConcurrencyMaxIdleConnsPerHost = 64

	// DefaultHighConcurrencyIdleConnTimeout defines how long idle keep-alive connections remain open (90s).
	DefaultHighConcurrencyIdleConnTimeout = 90 * time.Second
)

var (
	directHighConcurrencyTransportOnce sync.Once
	directHighConcurrencyTransport     *http.Transport
)

// TuneHighConcurrencyTransport tunes an existing HTTP transport for high-concurrency connection pooling.
func TuneHighConcurrencyTransport(transport *http.Transport) {
	if transport == nil {
		return
	}
	if transport.MaxIdleConns < DefaultHighConcurrencyMaxIdleConns {
		transport.MaxIdleConns = DefaultHighConcurrencyMaxIdleConns
	}
	if transport.MaxIdleConnsPerHost < DefaultHighConcurrencyMaxIdleConnsPerHost {
		transport.MaxIdleConnsPerHost = DefaultHighConcurrencyMaxIdleConnsPerHost
	}
	if transport.IdleConnTimeout <= 0 {
		transport.IdleConnTimeout = DefaultHighConcurrencyIdleConnTimeout
	}
}

func getSharedDirectHighConcurrencyTransport() *http.Transport {
	directHighConcurrencyTransportOnce.Do(func() {
		if tr, ok := http.DefaultTransport.(*http.Transport); ok && tr != nil {
			directHighConcurrencyTransport = tr.Clone()
		} else {
			directHighConcurrencyTransport = &http.Transport{}
		}
		directHighConcurrencyTransport.Proxy = nil
		directHighConcurrencyTransport.ForceAttemptHTTP2 = true
		TuneHighConcurrencyTransport(directHighConcurrencyTransport)
	})
	return directHighConcurrencyTransport
}

// NewHighConcurrencyHTTPClient creates an HTTP client tuned for high-concurrency pooling.
// When direct connection is used (no proxy and no context RoundTripper), it provides
// a shared direct transport with expanded MaxIdleConnsPerHost to prevent connection thrashing.
func NewHighConcurrencyHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) *http.Client {
	client := NewProxyAwareHTTPClient(ctx, cfg, auth, timeout)
	if client.Transport == nil {
		client.Transport = getSharedDirectHighConcurrencyTransport()
	} else if tr, ok := client.Transport.(*http.Transport); ok && tr != nil {
		TuneHighConcurrencyTransport(tr)
	}
	return client
}

// NewProxyAwareHTTPClient creates an HTTP client with proper proxy configuration priority:
// 1. Use auth.ProxyURL if configured (highest priority)
// 2. Use cfg.ProxyURL if auth proxy is not configured
// 3. Use RoundTripper from context if neither are configured
//
// Parameters:
//   - ctx: The context containing optional RoundTripper
//   - cfg: The application configuration
//   - auth: The authentication information
//   - timeout: The client timeout (0 means no timeout)
//
// Returns:
//   - *http.Client: An HTTP client with configured proxy or transport
func NewProxyAwareHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) *http.Client {
	httpClient := &http.Client{}
	if timeout > 0 {
		httpClient.Timeout = timeout
	}

	// Priority 1: Use auth.ProxyURL if configured
	var proxyURL string
	if auth != nil {
		proxyURL = strings.TrimSpace(auth.ProxyURL)
	}

	// Priority 2: Use cfg.ProxyURL if auth proxy is not configured
	if proxyURL == "" && cfg != nil {
		proxyURL = strings.TrimSpace(cfg.ProxyURL)
	}

	// If we have a proxy URL configured, set up the transport
	if proxyURL != "" {
		transport := buildProxyTransport(proxyURL)
		if transport != nil {
			httpClient.Transport = transport
			return httpClient
		}
		// If proxy setup failed, log and fall through to context RoundTripper
		log.Debugf("failed to setup proxy from URL: %s, falling back to context transport", proxyutil.Redact(proxyURL))
	}

	// Priority 3: Use RoundTripper from context (typically from RoundTripperFor)
	if rt, ok := ctx.Value("cliproxy.roundtripper").(http.RoundTripper); ok && rt != nil {
		httpClient.Transport = rt
	}

	return httpClient
}

// buildProxyTransport creates an HTTP transport configured for the given proxy URL.
// It supports SOCKS5, HTTP, and HTTPS proxy protocols.
//
// Parameters:
//   - proxyURL: The proxy URL string (e.g., "socks5://user:pass@host:port", "http://host:port")
//
// Returns:
//   - *http.Transport: A configured transport, or nil if the proxy URL is invalid
func buildProxyTransport(proxyURL string) *http.Transport {
	transport, _, errBuild := proxyutil.BuildHTTPTransport(proxyURL)
	if errBuild != nil {
		log.Errorf("%v", errBuild)
		return nil
	}
	TuneHighConcurrencyTransport(transport)
	return transport
}

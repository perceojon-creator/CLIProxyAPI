package helps

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestNewProxyAwareHTTPClientDirectBypassesGlobalProxy(t *testing.T) {
	t.Parallel()

	client := NewProxyAwareHTTPClient(
		context.Background(),
		&config.Config{SDKConfig: sdkconfig.SDKConfig{ProxyURL: "http://global-proxy.example.com:8080"}},
		&cliproxyauth.Auth{ProxyURL: "direct"},
		0,
	)

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("expected direct transport to disable proxy function")
	}
}

func TestTuneHighConcurrencyTransport(t *testing.T) {
	t.Parallel()

	tr := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 2,
	}
	TuneHighConcurrencyTransport(tr)

	if tr.MaxIdleConns != DefaultHighConcurrencyMaxIdleConns {
		t.Fatalf("MaxIdleConns = %d, want %d", tr.MaxIdleConns, DefaultHighConcurrencyMaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != DefaultHighConcurrencyMaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, want %d", tr.MaxIdleConnsPerHost, DefaultHighConcurrencyMaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout != DefaultHighConcurrencyIdleConnTimeout {
		t.Fatalf("IdleConnTimeout = %v, want %v", tr.IdleConnTimeout, DefaultHighConcurrencyIdleConnTimeout)
	}
}

func TestNewHighConcurrencyHTTPClient_Direct(t *testing.T) {
	t.Parallel()

	client := NewHighConcurrencyHTTPClient(context.Background(), nil, nil, 10*time.Second)
	if client.Timeout != 10*time.Second {
		t.Fatalf("Timeout = %v, want 10s", client.Timeout)
	}
	if client.Transport == nil {
		t.Fatal("expected non-nil transport for high-concurrency direct client")
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.Transport)
	}
	if tr.MaxIdleConnsPerHost < DefaultHighConcurrencyMaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost = %d, want >= %d", tr.MaxIdleConnsPerHost, DefaultHighConcurrencyMaxIdleConnsPerHost)
	}
	if tr.MaxIdleConns < DefaultHighConcurrencyMaxIdleConns {
		t.Fatalf("MaxIdleConns = %d, want >= %d", tr.MaxIdleConns, DefaultHighConcurrencyMaxIdleConns)
	}
}

func TestNewHighConcurrencyHTTPClient_ContextRoundTripper(t *testing.T) {
	t.Parallel()

	customTR := &http.Transport{}
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", http.RoundTripper(customTR))

	client := NewHighConcurrencyHTTPClient(ctx, nil, nil, 0)
	if client.Transport != customTR {
		t.Fatal("expected context roundtripper to be preserved")
	}
	if customTR.MaxIdleConnsPerHost < DefaultHighConcurrencyMaxIdleConnsPerHost {
		t.Fatalf("expected context transport to be tuned, got %d", customTR.MaxIdleConnsPerHost)
	}
}

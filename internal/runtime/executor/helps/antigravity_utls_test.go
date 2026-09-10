package helps

import (
	"context"
	cryptotls "crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tls "github.com/refraction-networking/utls"
)

func TestAntigravityTLSClientHelloSpec_NoALPNAndBoringSSL(t *testing.T) {
	spec := AntigravityTLSClientHelloSpec()
	if spec == nil {
		t.Fatal("expected non-nil ClientHelloSpec")
	}

	// 1. Verify ALPN is completely omitted (native Antigravity wire signature)
	for _, ext := range spec.Extensions {
		if _, isALPN := ext.(*tls.ALPNExtension); isALPN {
			t.Errorf("spec must NOT contain ALPNExtension; native Antigravity sends no ALPN on HTTP/1.1")
		}
	}

	// 2. Verify TLS 1.3 ciphers are present
	hasTLS13GCM := false
	for _, cs := range spec.CipherSuites {
		if cs == tls.TLS_AES_128_GCM_SHA256 {
			hasTLS13GCM = true
			break
		}
	}
	if !hasTLS13GCM {
		t.Errorf("expected TLS_AES_128_GCM_SHA256 in cipher suites")
	}
}

func TestAntigravityDialTLSContext_WireExecution(t *testing.T) {
	var (
		clientHelloProtos []string
	)

	// Create a TLS server to capture ClientHello and verify handshake
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &cryptotls.Config{
		MinVersion: cryptotls.VersionTLS12,
		MaxVersion: cryptotls.VersionTLS13,
		GetConfigForClient: func(hello *cryptotls.ClientHelloInfo) (*cryptotls.Config, error) {
			clientHelloProtos = append([]string(nil), hello.SupportedProtos...)
			return nil, nil
		},
	}
	srv.StartTLS()
	defer srv.Close()

	dialTLS := NewAntigravityDialTLSContext(nil, &cryptotls.Config{InsecureSkipVerify: true})
	transport := &http.Transport{
		ForceAttemptHTTP2: false,
		DialTLSContext:    dialTLS,
	}
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequestWithContext(context.Background(), "POST", srv.URL+"/v1internal:streamGenerateContent", strings.NewReader(`{"test":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-tok")
	req.Header.Set("User-Agent", "antigravity/hub/2.9.1 darwin/arm64")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("X-Goog-Api-Client", "antigravity-test")

	resp, errDo := client.Do(req)
	if errDo != nil {
		t.Fatalf("client.Do failed: %v", errDo)
	}
	defer resp.Body.Close()

	// 1. Verify ALPN is empty on wire (native Antigravity fingerprint)
	if len(clientHelloProtos) != 0 {
		t.Errorf("ClientHello ALPN = %v, expected none", clientHelloProtos)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 OK, got: %d", resp.StatusCode)
	}
}

func TestAntigravityHeaderOrderFunc_Ordering(t *testing.T) {
	order := AntigravityHeaderOrderFunc("POST", "/v1internal:streamGenerateContent")
	if len(order) < 5 {
		t.Fatalf("too few headers in order: %v", order)
	}
	if order[0] != "Host" {
		t.Errorf("first header must be Host, got: %s", order[0])
	}
	if order[1] != "User-Agent" {
		t.Errorf("second header must be User-Agent, got: %s", order[1])
	}
	if order[2] != "Authorization" {
		t.Errorf("third header must be Authorization, got: %s", order[2])
	}
	if order[3] != "Content-Type" {
		t.Errorf("fourth header must be Content-Type, got: %s", order[3])
	}
}

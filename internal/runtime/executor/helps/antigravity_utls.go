package helps

import (
	"context"
	cryptotls "crypto/tls"
	"fmt"
	"net"

	tls "github.com/refraction-networking/utls"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/httpwire"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/proxy"
)

// AntigravityRequestHeaderOrder defines the precise HTTP/1.1 header sequence emitted by
// the native Antigravity Electron/Node.js client.
var AntigravityRequestHeaderOrder = []string{
	"Host",
	"User-Agent",
	"Authorization",
	"Content-Type",
	"Accept",
	"Accept-Encoding",
	"X-Goog-Api-Client",
	"Connection",
	"Content-Length",
}

// AntigravityHeaderOrderFunc returns the header order for Antigravity requests.
func AntigravityHeaderOrderFunc(_, _ string) []string {
	return AntigravityRequestHeaderOrder
}

// AntigravityTLSClientHelloSpec reproduces the BoringSSL/Chromium TLS 1.3 ClientHello
// fingerprint emitted by Antigravity Hub on desktop (macOS/Windows/Linux).
// It deliberately omits the ALPN extension matching the native client stack.
func AntigravityTLSClientHelloSpec() *tls.ClientHelloSpec {
	return &tls.ClientHelloSpec{
		CipherSuites: []uint16{
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		},
		CompressionMethods: []uint8{0},
		Extensions: []tls.TLSExtension{
			&tls.SNIExtension{},
			&tls.ExtendedMasterSecretExtension{},
			&tls.RenegotiationInfoExtension{Renegotiation: tls.RenegotiateOnceAsClient},
			&tls.SupportedCurvesExtension{Curves: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384}},
			&tls.SupportedPointsExtension{SupportedPoints: []byte{0}},
			&tls.SessionTicketExtension{},
			// Deliberately no ALPNExtension: native Antigravity sends no ALPN extension on HTTP/1.1.
			&tls.StatusRequestExtension{},
			&tls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: []tls.SignatureScheme{
				tls.ECDSAWithP256AndSHA256,
				tls.PSSWithSHA256,
				tls.PKCS1WithSHA256,
				tls.ECDSAWithP384AndSHA384,
				tls.PSSWithSHA384,
				tls.PKCS1WithSHA384,
				tls.PSSWithSHA512,
				tls.PKCS1WithSHA512,
				tls.PKCS1WithSHA1,
			}},
			&tls.SCTExtension{},
			&tls.KeyShareExtension{KeyShares: []tls.KeyShare{{Group: tls.X25519}}},
			&tls.PSKKeyExchangeModesExtension{Modes: []uint8{tls.PskModeDHE}},
			&tls.SupportedVersionsExtension{Versions: []uint16{tls.VersionTLS13, tls.VersionTLS12}},
			&tls.UtlsPaddingExtension{GetPaddingLen: tls.BoringPaddingStyle},
			&tls.UtlsPreSharedKeyExtension{},
		},
	}
}

// NewAntigravityDialTLSContext returns a DialTLSContext function that wraps outgoing connections
// with the Chromium/Electron uTLS fingerprint and HTTP/1.1 wire header ordering.
func NewAntigravityDialTLSContext(dialer proxy.Dialer, baseTLSConfig *cryptotls.Config) func(ctx context.Context, network, addr string) (net.Conn, error) {
	sessionCache := tls.NewLRUClientSessionCache(64)
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		var (
			conn net.Conn
			err  error
		)
		if dialer == nil {
			dialer = proxy.Direct
		}
		if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
			conn, err = contextDialer.DialContext(ctx, network, addr)
		} else {
			conn, err = dialer.Dial(network, addr)
		}
		if err != nil {
			return nil, fmt.Errorf("antigravity utls: dial upstream: %w", err)
		}

		host, _, errSplit := net.SplitHostPort(addr)
		if errSplit != nil {
			if errClose := conn.Close(); errClose != nil {
				log.Debugf("antigravity utls: close failed connection: %v", errClose)
			}
			return nil, fmt.Errorf("antigravity utls: split upstream address: %w", errSplit)
		}

		serverName := host
		insecureSkipVerify := false
		if baseTLSConfig != nil {
			if baseTLSConfig.ServerName != "" {
				serverName = baseTLSConfig.ServerName
			}
			insecureSkipVerify = baseTLSConfig.InsecureSkipVerify
		}

		tlsConfig := &tls.Config{
			ServerName:                         serverName,
			ClientSessionCache:                 sessionCache,
			InsecureSkipVerify:                 insecureSkipVerify,
			OmitEmptyPsk:                       true,
			PreferSkipResumptionOnNilExtension: true,
		}
		if baseTLSConfig != nil && baseTLSConfig.RootCAs != nil {
			tlsConfig.RootCAs = baseTLSConfig.RootCAs
		}

		tlsConn := tls.UClient(conn, tlsConfig, tls.HelloCustom)
		if errPreset := tlsConn.ApplyPreset(AntigravityTLSClientHelloSpec()); errPreset != nil {
			if errClose := tlsConn.Close(); errClose != nil {
				log.Debugf("antigravity utls: close connection after preset failure: %v", errClose)
			}
			return nil, fmt.Errorf("antigravity utls: apply ClientHello spec: %w", errPreset)
		}

		if errHandshake := tlsConn.HandshakeContext(ctx); errHandshake != nil {
			if errClose := tlsConn.Close(); errClose != nil {
				log.Debugf("antigravity utls: close connection after handshake failure: %v", errClose)
			}
			return nil, fmt.Errorf("antigravity utls: handshake upstream: %w", errHandshake)
		}

		return httpwire.NewOrderedRequestConn(tlsConn, AntigravityHeaderOrderFunc), nil
	}
}

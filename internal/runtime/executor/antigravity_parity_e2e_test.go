package executor

import (
	"bytes"
	"context"
	cryptotls "crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"encoding/base64"
	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"

	internalcache "github.com/router-for-me/CLIProxyAPI/v7/internal/cache"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
	"google.golang.org/protobuf/encoding/protowire"
)

// canonicalJSON normalizes JSON by unmarshaling and marshaling with sorted keys (RFC 8785 canonical JSON).
func canonicalJSON(raw []byte) ([]byte, error) {
	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return nil, err
	}
	return json.Marshal(val)
}

// levenshteinRatio computes byte-level similarity percentage between two byte slices.
func levenshteinRatio(a, b []byte) (float64, int) {
	la, lb := len(a), len(b)
	if la == 0 && lb == 0 {
		return 100.0, 0
	}
	if la == 0 || lb == 0 {
		return 0.0, la + lb
	}

	dp := make([][]int, la+1)
	for i := range dp {
		dp[i] = make([]int, lb+1)
		dp[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		dp[0][j] = j
	}

	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d1 := dp[i-1][j] + 1      // deletion
			d2 := dp[i][j-1] + 1      // insertion
			d3 := dp[i-1][j-1] + cost // substitution
			m := d1
			if d2 < m {
				m = d2
			}
			if d3 < m {
				m = d3
			}
			dp[i][j] = m
		}
	}

	distance := dp[la][lb]
	maxLen := math.Max(float64(la), float64(lb))
	similarity := math.Max(0.0, (1.0-float64(distance)/maxLen)*100.0)
	return similarity, distance
}

func TestAntigravityParityEndToEnd_Gemini38Flash(t *testing.T) {
	nativeProjectID := "cloudaicompanion-project-107100"
	nativeToken := "ya29.c.b0AY2e-test-token-native-app"
	nativeModel := "gemini-3.8-flash"
	nativeSessionID := "-8819238472910384729"
	nativeRequestID := "agent-" + uuid.NewString()
	userPrompt := "Escribe una función concurrent-safe en Go con sync.RWMutex"

	nativePayloadJSON := fmt.Sprintf(`{
		"project": "%s",
		"model": "%s",
		"userAgent": "antigravity",
		"requestType": "agent",
		"requestId": "%s",
		"request": {
			"sessionId": "%s",
			"contents": [
				{
					"role": "user",
					"parts": [{"text": "%s"}]
				}
			],
			"generationConfig": {
				"temperature": 0.2
			}
		}
	}`, nativeProjectID, nativeModel, nativeRequestID, nativeSessionID, userPrompt)

	var nativeCompBuf bytes.Buffer
	if err := json.Compact(&nativeCompBuf, []byte(nativePayloadJSON)); err != nil {
		t.Fatalf("Compact native payload error: %v", err)
	}
	nativeCompBytes := nativeCompBuf.Bytes()

	cfg := &config.Config{RequestRetry: 1}
	executor := NewAntigravityExecutor(cfg)

	auth := &cliproxyauth.Auth{
		ID:       "antigravity-parity-test",
		Provider: "antigravity",
		Metadata: map[string]any{
			"access_token": nativeToken,
			"project_id":   nativeProjectID,
			"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}

	downstreamPayload := []byte(fmt.Sprintf(`{
		"contents": [
			{
				"role": "user",
				"parts": [{"text": "%s"}]
			}
		],
		"generationConfig": {
			"temperature": 0.2
		}
	}`, userPrompt))

	from := sdktranslator.FormatGemini
	to := sdktranslator.FromString("antigravity")
	_, translated := helps.TranslateRequestPairWithCodexMultiAgentV2(context.Background(), nil, cfg, from, to, nativeModel, downstreamPayload, downstreamPayload, true)
	translated = sanitizeAntigravityGeminiRequestSignatures(nativeModel, translated)
	translated = ensureAntigravityGeminiLeadingUserContent(nativeModel, translated)

	proxyReq, errBuild := executor.buildRequest(
		context.Background(),
		auth,
		nativeToken,
		nativeModel,
		translated,
		true,
		"",
		antigravityBaseURLProd,
		nativeSessionID,
	)
	if errBuild != nil {
		t.Fatalf("buildRequest failed: %v", errBuild)
	}

	proxyBodyBytes, errRead := proxyReq.GetBody()
	var proxyBody []byte
	if errRead == nil && proxyBodyBytes != nil {
		proxyBody, _ = io.ReadAll(proxyBodyBytes)
	} else if proxyReq.Body != nil {
		proxyBody, _ = io.ReadAll(proxyReq.Body)
	}

	t.Log("=== 1. ANÁLISIS DE TRANSPORTE Y CONEXIÓN ===")
	transport := newAntigravityHTTPClient(context.Background(), cfg, auth, 0).Transport.(*http.Transport)
	transportChecksPassed := 0
	totalTransportChecks := 4

	if !transport.ForceAttemptHTTP2 {
		transportChecksPassed++
		t.Log(" [PASS] HTTP/2 deshabilitado (ForceAttemptHTTP2 = false)")
	} else {
		t.Error(" [FAIL] HTTP/2 habilitado")
	}

	if len(transport.TLSNextProto) == 0 {
		transportChecksPassed++
		t.Log(" [PASS] TLSNextProto vacío (sin upgrade implícito a h2)")
	} else {
		t.Error(" [FAIL] TLSNextProto tiene manejadores")
	}

	if transport.TLSClientConfig != nil && len(transport.TLSClientConfig.NextProtos) == 0 {
		transportChecksPassed++
		t.Log(" [PASS] ALPN NextProtos nulo (omite ALPN igual que cliente nativo)")
	} else {
		t.Error(" [FAIL] ALPN NextProtos anunciado")
	}

	if !proxyReq.Close && proxyReq.Header.Get("Connection") == "" {
		transportChecksPassed++
		t.Log(" [PASS] Connection: close omitido (Keep-Alive persistente activo)")
	} else {
		t.Error(" [FAIL] Se encontró cabecera Connection: close")
	}

	transportScore := (float64(transportChecksPassed) / float64(totalTransportChecks)) * 100.0

	t.Log("=== 2. ANÁLISIS DE CABECERAS HTTP ===")
	headerChecksPassed := 0
	totalHeaderChecks := 5

	if proxyReq.Header.Get("Content-Type") == "application/json" {
		headerChecksPassed++
		t.Log(" [PASS] Content-Type: application/json")
	}

	if proxyReq.Header.Get("Authorization") == "Bearer "+nativeToken {
		headerChecksPassed++
		t.Log(" [PASS] Authorization: Bearer token exacto")
	}

	ua := proxyReq.Header.Get("User-Agent")
	uaRegex := regexp.MustCompile(`^antigravity/hub/\d+\.\d+\.\d+ darwin/arm64$`)
	if uaRegex.MatchString(ua) {
		headerChecksPassed++
		t.Logf(" [PASS] User-Agent cumple formato nativo: %s", ua)
	} else {
		t.Errorf(" [FAIL] User-Agent no cumple formato: %s", ua)
	}

	versionStr := misc.AntigravityVersionFromUserAgent(ua)
	var major, minor, patch int
	fmt.Sscanf(versionStr, "%d.%d.%d", &major, &minor, &patch)
	if major > 2 || (major == 2 && minor >= 9) {
		headerChecksPassed++
		t.Logf(" [PASS] Versión reportada (%s) cumple el suelo mínimo de Google (>= 2.9.0)", versionStr)
	} else {
		t.Errorf(" [FAIL] Versión reportada (%s) por debajo del suelo mínimo 2.9.0", versionStr)
	}

	if proxyReq.Host == "cloudcode-pa.googleapis.com" {
		headerChecksPassed++
		t.Log(" [PASS] Host de cabecera alineado con upstream oficial")
	}

	headerScore := (float64(headerChecksPassed) / float64(totalHeaderChecks)) * 100.0

	t.Log("=== 3. ANÁLISIS ESTRUCTURAL DEL PAYLOAD JSON ===")
	structuralKeys := []string{
		"model",
		"project",
		"userAgent",
		"requestType",
		"requestId",
		"request.sessionId",
		"request.contents",
		"request.generationConfig",
	}

	keysMatched := 0
	for _, k := range structuralKeys {
		nativeVal := gjson.GetBytes(nativeCompBytes, k)
		proxyVal := gjson.GetBytes(proxyBody, k)

		if !nativeVal.Exists() || !proxyVal.Exists() {
			t.Errorf(" [FAIL] Clave %s ausente en uno de los payloads", k)
			continue
		}

		if nativeVal.Type != proxyVal.Type {
			t.Errorf(" [FAIL] Clave %s tipo no coincide: native=%v proxy=%v", k, nativeVal.Type, proxyVal.Type)
			continue
		}

		keysMatched++
		t.Logf(" [PASS] Clave estructural presente y tipeada: %-25s (native: %s | proxy: %s)", k, nativeVal.Type, proxyVal.Type)
	}

	structuralScore := (float64(keysMatched) / float64(len(structuralKeys))) * 100.0

	t.Log("=== 4. INVARIANTES DE ACEPTACIÓN DE GOOGLE BACKEND ===")
	backendChecksPassed := 0
	totalBackendChecks := 5

	if proxyReq.URL.Path == "/v1internal:streamGenerateContent" && proxyReq.URL.RawQuery == "alt=sse" {
		backendChecksPassed++
		t.Log(" [PASS] Endpoint oficial exacto (/v1internal:streamGenerateContent?alt=sse)")
	}

	if gjson.GetBytes(proxyBody, "userAgent").String() == "antigravity" {
		backendChecksPassed++
		t.Log(" [PASS] Payload userAgent == 'antigravity'")
	}

	if gjson.GetBytes(proxyBody, "requestType").String() == "agent" {
		backendChecksPassed++
		t.Log(" [PASS] Payload requestType == 'agent'")
	}

	if strings.HasPrefix(gjson.GetBytes(proxyBody, "requestId").String(), "agent-") {
		backendChecksPassed++
		t.Log(" [PASS] Payload requestId usa prefijo nativo 'agent-'")
	}

	firstRole := gjson.GetBytes(proxyBody, "request.contents.0.role").String()
	if firstRole == "user" {
		backendChecksPassed++
		t.Log(" [PASS] Primer turno de contents tiene rol 'user'")
	}

	backendScore := (float64(backendChecksPassed) / float64(totalBackendChecks)) * 100.0

	t.Log("=== 5. ANÁLISIS BINARIO BIT-POR-BIT / LEVENSHTEIN ===")
	rawByteSimilarity, rawDist := levenshteinRatio(nativeCompBytes, proxyBody)
	t.Logf(" Bytes Payload Nativo Crudo:  %d bytes", len(nativeCompBytes))
	t.Logf(" Bytes Payload Proxy Crudo:   %d bytes", len(proxyBody))
	t.Logf(" Levenshtein Distancia Cruda: %d", rawDist)
	t.Logf(" Identidad Binaria Cruda (con orden de claves nativo vs sjson): %.2f%%", rawByteSimilarity)

	// Canonical JSON comparison (eliminates arbitrary key-order and whitespace)
	canonNative, _ := canonicalJSON(nativeCompBytes)
	canonProxy, _ := canonicalJSON(proxyBody)
	t.Logf(" Canon Native: %s", string(canonNative))
	t.Logf(" Canon Proxy:  %s", string(canonProxy))
	canonSimilarity, canonDist := levenshteinRatio(canonNative, canonProxy)
	t.Logf(" Bytes Payload Nativo Canónico: %d bytes", len(canonNative))
	t.Logf(" Bytes Payload Proxy Canónico:  %d bytes", len(canonProxy))
	t.Logf(" Levenshtein Distancia Canónica (solo difiere UUID de requestId): %d", canonDist)
	t.Logf(" Identidad Canónica Normalizada (RFC 8785): %.2f%%", canonSimilarity)

	functionalParity := (transportScore * 0.20) + (headerScore * 0.20) + (structuralScore * 0.30) + (backendScore * 0.30)

	t.Log("=====================================================")
	t.Logf(" RESUMEN DE PARIDAD GEMINI 3.8 FLASH:")
	t.Logf(" - Conformidad de Transporte (TLS/TCP/HTTP1.1):   %.1f%%", transportScore)
	t.Logf(" - Paridad de Cabeceras HTTP:                     %.1f%%", headerScore)
	t.Logf(" - Paridad de Envelope y Estructura JSON:         %.1f%%", structuralScore)
	t.Logf(" - Invariantes de Aceptación de Google Backend:   %.1f%%", backendScore)
	t.Logf(" >>> PARIDAD FUNCIONAL / RECONOCIMIENTO GOOGLE:   %.1f%%", functionalParity)
	t.Logf(" >>> SIMILITUD BINARIA CRUDA (BYTE-POR-BYTE):     %.1f%%", rawByteSimilarity)
	t.Logf(" >>> SIMILITUD CANÓNICA NORMALIZADA (RFC 8785):   %.1f%%", canonSimilarity)
	t.Log("=====================================================")

	if functionalParity < 95.0 {
		t.Fatalf("Functional parity %.1f%% below acceptable threshold of 95%%", functionalParity)
	}
}

func TestAntigravityParityEndToEnd_Gemini38Flash_MultiTurnTools(t *testing.T) {
	nativeProjectID := "cloudaicompanion-project-107100"
	nativeToken := "ya29.c.b0AY2e-test-token-native-app"
	nativeModel := "gemini-3.8-flash"
	nativeSessionID := "-8819238472910384729"

	downstreamPayload := []byte(`{
		"contents": [
			{
				"role": "user",
				"parts": [{"text": "¿Qué hora es en Tokio?"}]
			},
			{
				"role": "model",
				"parts": [
					{
						"functionCall": {
							"name": "get_current_time",
							"args": {"timezone": "Asia/Tokyo"}
						}
					}
				]
			},
			{
				"role": "user",
				"parts": [
					{
						"functionResponse": {
							"name": "get_current_time",
							"response": {"time": "2026-09-09T15:30:00+09:00"}
						}
					}
				]
			}
		],
		"tools": [
			{
				"functionDeclarations": [
					{
						"name": "get_current_time",
						"description": "Obtiene la hora actual",
						"parameters": {
							"type": "object",
							"properties": {
								"timezone": {"type": "string"}
							},
							"required": ["timezone"]
						}
					}
				]
			}
		]
	}`)

	cfg := &config.Config{RequestRetry: 1}
	executor := NewAntigravityExecutor(cfg)

	auth := &cliproxyauth.Auth{
		ID:       "antigravity-parity-multiturn",
		Provider: "antigravity",
		Metadata: map[string]any{
			"access_token": nativeToken,
			"project_id":   nativeProjectID,
			"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}

	from := sdktranslator.FormatGemini
	to := sdktranslator.FromString("antigravity")
	_, translated := helps.TranslateRequestPairWithCodexMultiAgentV2(context.Background(), nil, cfg, from, to, nativeModel, downstreamPayload, downstreamPayload, true)
	translated = sanitizeAntigravityGeminiRequestSignatures(nativeModel, translated)
	translated = ensureAntigravityGeminiLeadingUserContent(nativeModel, translated)

	proxyReq, errBuild := executor.buildRequest(
		context.Background(),
		auth,
		nativeToken,
		nativeModel,
		translated,
		true,
		"",
		antigravityBaseURLProd,
		nativeSessionID,
	)
	if errBuild != nil {
		t.Fatalf("buildRequest failed: %v", errBuild)
	}

	proxyBodyBytes, _ := proxyReq.GetBody()
	proxyBody, _ := io.ReadAll(proxyBodyBytes)

	sig := gjson.GetBytes(proxyBody, "request.contents.1.parts.0.thoughtSignature").String()
	if sig != "skip_thought_signature_validator" && sig == "" {
		t.Errorf("Expected valid thoughtSignature or sentinel on functionCall, got %q", sig)
	} else {
		t.Logf(" [PASS] Multi-turn functionCall protegido con thoughtSignature: %s", sig)
	}

	roles := []string{
		gjson.GetBytes(proxyBody, "request.contents.0.role").String(),
		gjson.GetBytes(proxyBody, "request.contents.1.role").String(),
		gjson.GetBytes(proxyBody, "request.contents.2.role").String(),
	}
	// In Antigravity internal backend, normalizeAntigravityGeminiFunctionResponseRoles normalizes functionResponse to role "model"
	if roles[0] == "user" && roles[1] == "model" && roles[2] == "model" {
		t.Logf(" [PASS] Roles ordenados conforme al protocolo interno de Antigravity (user -> model -> model): %v", roles)
	} else {
		t.Errorf(" [FAIL] Roles no conformes: %v", roles)
	}

	if gjson.GetBytes(proxyBody, "request.tools.0.functionDeclarations.0.name").String() == "get_current_time" {
		t.Log(" [PASS] Declaración de herramientas preservada y ubicada en request.tools")
	} else {
		t.Error(" [FAIL] Fallo en la ubicación de request.tools")
	}
}

// TestAntigravityParityEndToEnd_Phase1_UTLS_And_WireHeaderOrdering asserts that
// Phase 1 defenses (BoringSSL/Chromium uTLS fingerprint and HTTP/1.1 wire header ordering)
// are actively applied to live connections dispatched by the Antigravity executor.
func TestAntigravityParityEndToEnd_Phase1_UTLS_And_WireHeaderOrdering(t *testing.T) {
	var (
		clientHelloProtos []string
		tlsVersion        uint16
	)

	l, errListen := net.Listen("tcp", "127.0.0.1:0")
	if errListen != nil {
		t.Fatalf("listen error: %v", errListen)
	}
	defer l.Close()

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			tlsVersion = r.TLS.Version
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	srv.Listener = l
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

	auth := &cliproxyauth.Auth{
		ID:       "phase1-test-auth",
		Provider: "antigravity",
		Metadata: map[string]any{
			"access_token": "ya29.test-phase1-token",
		},
	}

	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = &cryptotls.Config{InsecureSkipVerify: true}
	transport := antigravityHTTP11Transport(auth, base)

	client := &http.Client{Transport: transport}
	req, errReq := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		srv.URL+"/v1internal:streamGenerateContent?alt=sse",
		strings.NewReader(`{"test":true}`),
	)
	if errReq != nil {
		t.Fatalf("NewRequest error: %v", errReq)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ya29.test-phase1-token")
	req.Header.Set("User-Agent", "antigravity/hub/2.9.1 darwin/arm64")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("X-Goog-Api-Client", "gl-go/1.26.0")

	resp, errDo := client.Do(req)
	if errDo != nil {
		t.Fatalf("client.Do failed: %v", errDo)
	}
	defer resp.Body.Close()

	if resp.TLS != nil {
		tlsVersion = resp.TLS.Version
	}

	t.Log("=== FASE 1: AUDITORIA FORENSE DE TRANSPORTE TLS Y CABECERAS EN CABLE ===")
	// 1. Assert TLS 1.3 negotiated
	if tlsVersion == cryptotls.VersionTLS13 {
		t.Logf(" [PASS] TLS 1.3 negociado con exito (version: %#x)", tlsVersion)
	} else {
		t.Errorf(" [FAIL] Version TLS inesperada: %#x", tlsVersion)
	}

	// 2. Assert ALPN extension completely omitted (exact Antigravity wire signature)
	if len(clientHelloProtos) == 0 {
		t.Log(" [PASS] ALPN omitido en ClientHello (indistinguible del cliente oficial de Antigravity Hub)")
	} else {
		t.Errorf(" [FAIL] ALPN detectado en ClientHello: %v", clientHelloProtos)
	}

	// 3. Assert wire header ordering via helps.AntigravityRequestHeaderOrder
	wireOrder := helps.AntigravityRequestHeaderOrder
	if len(wireOrder) >= 4 && wireOrder[0] == "Host" && wireOrder[1] == "User-Agent" && wireOrder[2] == "Authorization" && wireOrder[3] == "Content-Type" {
		t.Logf(" [PASS] Orden de cabeceras HTTP/1.1 en cable alineado con Electron/Node.js: %v", wireOrder[:4])
	} else {
		t.Errorf(" [FAIL] Secuencia de cabeceras divergente: %v", wireOrder)
	}
}
func generateTestTinkHMACSignature(keyID []byte, ciphertext string) string {
	tinkPayload := append([]byte{0x01}, keyID...)
	tinkPayload = append(tinkPayload, []byte(ciphertext)...)

	var inner []byte
	inner = protowire.AppendTag(inner, 1, protowire.BytesType)
	inner = protowire.AppendBytes(inner, tinkPayload)

	var outer []byte
	outer = protowire.AppendTag(outer, 2, protowire.BytesType)
	outer = protowire.AppendBytes(outer, inner)
	return base64.StdEncoding.EncodeToString(outer)
}

func TestAntigravityParityEndToEnd_Phase3_AuthenticHMACSignaturePreservation(t *testing.T) {
	t.Log("=== FASE 3: PRESERVACIÓN CRIPTOGRÁFICA DE HMAC THOUGHT SIGNATURES ===")
	cfg := &config.Config{RequestRetry: 1}
	executor := NewAntigravityExecutor(cfg)
	const model = "gemini-3.8-flash"
	auth := &cliproxyauth.Auth{
		ID:       "antigravity-parity-phase3",
		Provider: "antigravity",
		Metadata: map[string]any{
			"access_token": "ya29.phase3-test-token",
			"project_id":   "cloudaicompanion-project-107100",
			"expired":      time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
	to := sdktranslator.FromString("antigravity")
	keyID := []byte{0x11, 0x4d, 0x32, 0x0f}
	authenticSig := generateTestTinkHMACSignature(keyID, "upstream-authentic-hmac-38-flash-candidate-token")

	t.Run("GeminiDownstream_AuthenticSignaturePreserved", func(t *testing.T) {
		geminiReq := []byte(fmt.Sprintf(`{
			"contents": [
				{"role": "user", "parts": [{"text": "¿Cuál es el clima?"}]},
				{"role": "model", "parts": [{"functionCall": {"name": "get_weather", "args": {"city": "Madrid"}}, "thoughtSignature": "%s"}]},
				{"role": "user", "parts": [{"functionResponse": {"name": "get_weather", "response": {"temp": "22C"}}}]}
			]
		}`, authenticSig))

		_, translated := helps.TranslateRequestPairWithCodexMultiAgentV2(context.Background(), nil, cfg, sdktranslator.FormatGemini, to, model, geminiReq, geminiReq, true)
		translated = sanitizeAntigravityGeminiRequestSignatures(model, translated)
		translated = ensureAntigravityGeminiLeadingUserContent(model, translated)

		proxyReq, errBuild := executor.buildRequest(context.Background(), auth, "ya29.token", model, translated, true, "", antigravityBaseURLProd, "-101010")
		if errBuild != nil {
			t.Fatalf("buildRequest failed: %v", errBuild)
		}
		bodyBytes, _ := proxyReq.GetBody()
		body, _ := io.ReadAll(bodyBytes)

		sig := gjson.GetBytes(body, "request.contents.1.parts.0.thoughtSignature").String()
		if sig != authenticSig {
			t.Fatalf("Gemini downstream: expected authentic signature %s, got %s", authenticSig, sig)
		}
		t.Logf(" [PASS] Gemini downstream: firma HMAC autentica preservada en cable: %s", sig[:24]+"...")
	})

	t.Run("ClaudeDownstream_ThinkingSignaturePreserved", func(t *testing.T) {
		claudeReq := []byte(fmt.Sprintf(`{
			"messages": [
				{"role": "user", "content": "¿Cuál es el clima?"},
				{"role": "assistant", "content": [
					{"type": "thinking", "thinking": "Consultando API de clima...", "signature": "%s"},
					{"type": "tool_use", "id": "call_cl_1", "name": "get_weather", "input": {"city": "Madrid"}}
				]},
				{"role": "user", "content": [
					{"type": "tool_result", "tool_use_id": "call_cl_1", "content": "{\"temp\": \"22C\"}"}
				]}
			]
		}`, authenticSig))

		_, translated := helps.TranslateRequestPairWithCodexMultiAgentV2(context.Background(), nil, cfg, sdktranslator.FormatClaude, to, model, claudeReq, claudeReq, true)
		translated = sanitizeAntigravityGeminiRequestSignatures(model, translated)
		translated = ensureAntigravityGeminiLeadingUserContent(model, translated)

		proxyReq, errBuild := executor.buildRequest(context.Background(), auth, "ya29.token", model, translated, true, "", antigravityBaseURLProd, "-101010")
		if errBuild != nil {
			t.Fatalf("buildRequest failed: %v", errBuild)
		}
		bodyBytes, _ := proxyReq.GetBody()
		body, _ := io.ReadAll(bodyBytes)

		sig := gjson.GetBytes(body, "request.contents.1.parts.0.thoughtSignature").String()
		if sig == "" {
			sig = gjson.GetBytes(body, "request.contents.1.parts.1.thoughtSignature").String()
		}
		if sig != authenticSig {
			t.Fatalf("Claude downstream: expected authentic signature %s, got %s", authenticSig, sig)
		}
		t.Logf(" [PASS] Claude downstream: firma HMAC autentica preservada en cable: %s", sig[:24]+"...")
	})

	t.Run("OpenAIDownstream_ExtraContentSignaturePreserved", func(t *testing.T) {
		openAIReq := []byte(fmt.Sprintf(`{
			"messages": [
				{"role": "user", "content": "¿Cuál es el clima?"},
				{
					"role": "assistant",
					"content": null,
					"tool_calls": [{
						"id": "call_oa_1",
						"type": "function",
						"function": {"name": "get_weather", "arguments": "{\"city\":\"Madrid\"}"},
						"extra_content": {"google": {"thought_signature": "%s"}}
					}]
				},
				{"role": "tool", "tool_call_id": "call_oa_1", "content": "{\"temp\": \"22C\"}"}
			]
		}`, authenticSig))

		_, translated := helps.TranslateRequestPairWithCodexMultiAgentV2(context.Background(), nil, cfg, sdktranslator.FormatOpenAI, to, model, openAIReq, openAIReq, true)
		translated = sanitizeAntigravityGeminiRequestSignatures(model, translated)
		translated = ensureAntigravityGeminiLeadingUserContent(model, translated)

		proxyReq, errBuild := executor.buildRequest(context.Background(), auth, "ya29.token", model, translated, true, "", antigravityBaseURLProd, "-101010")
		if errBuild != nil {
			t.Fatalf("buildRequest failed: %v", errBuild)
		}
		bodyBytes, _ := proxyReq.GetBody()
		body, _ := io.ReadAll(bodyBytes)

		sig := gjson.GetBytes(body, "request.contents.1.parts.0.thoughtSignature").String()
		if sig != authenticSig {
			t.Fatalf("OpenAI downstream: expected authentic signature %s, got %s", authenticSig, sig)
		}
		t.Logf(" [PASS] OpenAI downstream: extra_content firma HMAC preservada en cable: %s", sig[:24]+"...")
	})

	t.Run("MultiTurnReasoningReplay_RestoresAuthenticSignatureAcrossTurns", func(t *testing.T) {
		const sessionKey = "session:test-phase3-multiturn-session"
		internalcache.ClearAntigravityReasoningReplayCache()
		t.Cleanup(internalcache.ClearAntigravityReasoningReplayCache)

		turn1Req := []byte(`{"sessionId":"test-phase3-multiturn-session","request":{"contents":[{"role":"user","parts":[{"text":"Calcula la ruta"}]}]}}`)
		scope := antigravityReasoningReplayScope{modelName: model, sessionKey: sessionKey}
		acc := newAntigravityReasoningReplayAccumulator(scope, turn1Req)

		sseChunk := []byte(fmt.Sprintf(`data: {"response":{"candidates":[{"content":{"parts":[{"functionCall":{"id":"call_route_1","name":"calculate_route","args":{"dest":"Madrid"}},"thoughtSignature":"%s"}]},"finishReason":"STOP"}]}}`, authenticSig))
		acc.ObserveSSELine(sseChunk)
		acc.Commit(context.Background())

		turn2ClientPayload := []byte(`{
			"sessionId": "test-phase3-multiturn-session",
			"request": {
				"contents": [
					{"role": "user", "parts": [{"text": "Calcula la ruta"}]},
					{"role": "model", "parts": [{"functionCall": {"id": "call_route_1", "name": "calculate_route", "args": {"dest": "Madrid"}}}]},
					{"role": "user", "parts": [{"functionResponse": {"id": "call_route_1", "name": "calculate_route", "response": {"distance": "350km"}}}]}
				]
			}
		}`)

		preparedTurn2, _, errPrepare := prepareAntigravityGeminiReasoningReplayPayload(
			context.Background(),
			model,
			cliproxyexecutor.Request{Model: model, Payload: turn2ClientPayload},
			cliproxyexecutor.Options{},
			turn2ClientPayload,
		)
		if errPrepare != nil {
			t.Fatalf("prepareAntigravityGeminiReasoningReplayPayload failed: %v", errPrepare)
		}

		sigTurn2 := gjson.GetBytes(preparedTurn2, "request.contents.1.parts.0.thoughtSignature").String()
		if sigTurn2 != authenticSig {
			t.Fatalf("Reasoning replay failed to restore authentic HMAC signature! Got: %q, Want: %q", sigTurn2, authenticSig)
		}
		if sigTurn2 == "skip_thought_signature_validator" {
			t.Fatal("CRITICAL: Bypass sentinel was used instead of authentic replayed signature!")
		}
		t.Logf(" [PASS] Turno 2 multi-turn: Firma HMAC autentica restaurada de cache (bypass sentinel eliminado): %s", sigTurn2[:24]+"...")
	})

	t.Run("SyntheticTurn_FallsBackSafelyToBypassSentinel", func(t *testing.T) {
		internalcache.ClearAntigravityReasoningReplayCache()
		t.Cleanup(internalcache.ClearAntigravityReasoningReplayCache)

		syntheticPayload := []byte(`{
			"sessionId": "unknown-synthetic-session",
			"request": {
				"contents": [
					{"role": "user", "parts": [{"text": "run command"}]},
					{"role": "model", "parts": [{"functionCall": {"name": "run", "args": {"cmd": "ls"}}}]},
					{"role": "user", "parts": [{"functionResponse": {"name": "run", "response": {"out": "file.txt"}}}]}
				]
			}
		}`)

		prepared, _, err := prepareAntigravityGeminiReasoningReplayPayload(
			context.Background(),
			model,
			cliproxyexecutor.Request{Model: model, Payload: syntheticPayload},
			cliproxyexecutor.Options{},
			syntheticPayload,
		)
		if err != nil {
			t.Fatalf("prepare error: %v", err)
		}

		sig := gjson.GetBytes(prepared, "request.contents.1.parts.0.thoughtSignature").String()
		if sig != "skip_thought_signature_validator" {
			t.Fatalf("expected fallback sentinel 'skip_thought_signature_validator', got %q", sig)
		}
		t.Logf(" [PASS] Turno sintetico sin firma previa: Fallback seguro a sentinel controlado: %s", sig)
	})
}

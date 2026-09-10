package helps

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestAlignAntigravityWireEnvelope_OrdersKeysAndCompacts(t *testing.T) {
	input := []byte(`{
		"request": {
			"generationConfig": { "temperature": 0.5 },
			"contents": [ {"role": "user", "parts": [{"text": "hello"}]} ],
			"sessionId": "-12345"
		},
		"requestId": "agent-123",
		"model": "gemini-3.8-flash",
		"project": "proj-1",
		"userAgent": "antigravity",
		"requestType": "agent"
	}`)

	got := AlignAntigravityWireEnvelope(input)
	gotStr := string(got)

	// Validate no whitespace/newlines outside strings
	if strings.Contains(gotStr, " ") || strings.Contains(gotStr, "\n") || strings.Contains(gotStr, "\t") {
		t.Errorf("Envelope should be strictly compact, got: %s", gotStr)
	}

	// Validate exact top-level order
	wantPrefix := `{"project":"proj-1","model":"gemini-3.8-flash","userAgent":"antigravity","requestType":"agent","requestId":"agent-123","request":{"sessionId":"-12345","contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"temperature":0.5}}}`
	var wantComp bytes.Buffer
	_ = json.Compact(&wantComp, []byte(wantPrefix))
	if gotStr != wantComp.String() {
		t.Fatalf("Mismatch!\nGot:  %s\nWant: %s", gotStr, wantComp.String())
	}
}

func TestAlignAntigravityWireEnvelope_HandlesEmptyOrMalformed(t *testing.T) {
	if res := AlignAntigravityWireEnvelope(nil); len(res) != 0 {
		t.Errorf("expected empty for nil, got %s", string(res))
	}
	if res := AlignAntigravityWireEnvelope([]byte("invalid json")); string(res) != "invalid json" {
		t.Errorf("expected original on malformed, got %s", string(res))
	}
	if res := AlignAntigravityWireEnvelope([]byte(`[1, 2, 3]`)); string(res) != `[1, 2, 3]` {
		t.Errorf("expected original on non-object, got %s", string(res))
	}
}

func TestAlignAntigravityWireEnvelope_PreservesExtraFields(t *testing.T) {
	input := []byte(`{"customRoot":"val","project":"p","request":{"customReq":"subval","contents":[]}}`)
	got := AlignAntigravityWireEnvelope(input)

	if gjson.GetBytes(got, "customRoot").String() != "val" {
		t.Errorf("customRoot was not preserved")
	}
	if gjson.GetBytes(got, "request.customReq").String() != "subval" {
		t.Errorf("request.customReq was not preserved")
	}
}

func BenchmarkAlignAntigravityWireEnvelope(b *testing.B) {
	payload := []byte(`{
		"request": {
			"contents": [{"role":"user","parts":[{"text":"Benchmarking memory and speed"}]}],
			"generationConfig": {"temperature": 0.2},
			"sessionId": "-999999999"
		},
		"model": "gemini-3.8-flash",
		"project": "bench-proj",
		"userAgent": "antigravity",
		"requestType": "agent",
		"requestId": "agent-bench-uuid"
	}`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = AlignAntigravityWireEnvelope(payload)
	}
}

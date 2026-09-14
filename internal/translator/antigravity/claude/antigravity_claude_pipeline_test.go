package claude

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestPipelineStage1_SystemPartsExtraction(t *testing.T) {
	// 1. Plain text system prompt
	raw1 := []byte(`{"system":"Be helpful and direct"}`)
	parts1 := extractAntigravityClaudeSystemParts(raw1)
	if len(parts1) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts1))
	}
	if gjson.GetBytes(parts1[0], "text").String() != "Be helpful and direct" {
		t.Errorf("unexpected text: %s", string(parts1[0]))
	}

	// 2. System array with attribution filtering
	raw2 := []byte(`{"system":[
		{"type":"text","text":"System rule 1"},
		{"type":"text","text":"x-anthropic-billing-header: cc_version=1.0"},
		{"type":"text","text":"System rule 2"}
	]}`)
	parts2 := extractAntigravityClaudeSystemParts(raw2)
	if len(parts2) != 2 {
		t.Fatalf("expected 2 parts after filtering attribution, got %d", len(parts2))
	}
	if gjson.GetBytes(parts2[0], "text").String() != "System rule 1" {
		t.Errorf("unexpected part 0: %s", string(parts2[0]))
	}
	if gjson.GetBytes(parts2[1], "text").String() != "System rule 2" {
		t.Errorf("unexpected part 1: %s", string(parts2[1]))
	}
}

func TestPipelineStage2_ToolsNormalization(t *testing.T) {
	raw := []byte(`{
		"tools": [
			{
				"name": "read_file",
				"description": "Read file contents",
				"input_schema": {
					"type": "object",
					"properties": {
						"path": {"type": "string"}
					},
					"required": ["path"]
				}
			}
		]
	}`)

	functionMap := map[string]string{}
	toolsJSON, count := extractAntigravityClaudeTools(raw, functionMap)
	if count != 1 {
		t.Fatalf("expected 1 tool, got %d", count)
	}
	if !strings.Contains(string(toolsJSON), "read_file") {
		t.Errorf("expected toolsJSON to contain read_file, got: %s", string(toolsJSON))
	}
	if !strings.Contains(string(toolsJSON), "parametersJsonSchema") {
		t.Errorf("expected parametersJsonSchema, got: %s", string(toolsJSON))
	}
}

func TestPipelineStage3_ContentsAndModalities(t *testing.T) {
	raw := []byte(`{
		"messages": [
			{"role": "user", "content": "What is 2+2?"},
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "Let me calculate", "signature": ""},
				{"type": "text", "text": "4"}
			]}
		]
	}`)

	functionMap := map[string]string{}
	items, enableThought := extractAntigravityClaudeContents("gemini-3.8-flash-high", raw, functionMap)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if !enableThought {
		t.Errorf("expected enableThought to be true for gemini-3.8-flash-high")
	}

	userMsg := gjson.ParseBytes(items[0])
	if userMsg.Get("role").String() != "user" {
		t.Errorf("expected user role, got: %s", userMsg.Get("role").String())
	}

	modelMsg := gjson.ParseBytes(items[1])
	if modelMsg.Get("role").String() != "model" {
		t.Errorf("expected model role, got: %s", modelMsg.Get("role").String())
	}
}

func TestPipelineStage4_PayloadAssembly(t *testing.T) {
	raw := []byte(`{
		"temperature": 0.7,
		"top_p": 0.9,
		"max_tokens": 1024
	}`)

	out := assembleAntigravityClaudePayload(
		"gemini-3.8-flash-high",
		raw,
		[][]byte{[]byte(`{"text":"System instruction"}`)},
		[][]byte{[]byte(`{"role":"user","parts":[{"text":"Hello"}]}`)},
		nil,
		0,
		map[string]string{},
		true,
	)

	parsed := gjson.ParseBytes(out)
	if parsed.Get("model").String() != "gemini-3.8-flash-high" {
		t.Errorf("unexpected model: %s", parsed.Get("model").String())
	}
	if parsed.Get("request.generationConfig.temperature").Float() != 0.7 {
		t.Errorf("unexpected temperature: %v", parsed.Get("request.generationConfig.temperature").Float())
	}
	if parsed.Get("request.generationConfig.maxOutputTokens").Int() != 1024 {
		t.Errorf("unexpected maxOutputTokens: %v", parsed.Get("request.generationConfig.maxOutputTokens").Int())
	}
	if parsed.Get("request.systemInstruction.parts.0.text").String() != "System instruction" {
		t.Errorf("unexpected systemInstruction: %s", parsed.Get("request.systemInstruction").Raw)
	}
}

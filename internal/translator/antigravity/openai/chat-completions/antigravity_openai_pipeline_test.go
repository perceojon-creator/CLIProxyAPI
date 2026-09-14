package chat_completions

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestPipelineStage1_InitEnvelope(t *testing.T) {
	rawJSON := []byte(`{
		"temperature": 0.8,
		"top_p": 0.9,
		"top_k": 30,
		"max_tokens": 512,
		"reasoning_effort": "auto",
		"n": 3,
		"modalities": ["image", "text"],
		"image_config": {"aspect_ratio": "4:3", "image_size": "512x512"},
		"response_format": {"type": "json_object"}
	}`)

	out := stage1InitAntigravityOpenAIEnvelope("gemini-2.5-flash", rawJSON)
	parsed := gjson.ParseBytes(out)

	if parsed.Get("model").String() != "gemini-2.5-flash" {
		t.Fatalf("expected model gemini-2.5-flash, got %s", parsed.Get("model").String())
	}
	if parsed.Get("request.generationConfig.temperature").Float() != 0.8 {
		t.Fatalf("expected temperature 0.8, got %f", parsed.Get("request.generationConfig.temperature").Float())
	}
	if parsed.Get("request.generationConfig.topP").Float() != 0.9 {
		t.Fatalf("expected topP 0.9, got %f", parsed.Get("request.generationConfig.topP").Float())
	}
	if parsed.Get("request.generationConfig.topK").Int() != 30 {
		t.Fatalf("expected topK 30, got %d", parsed.Get("request.generationConfig.topK").Int())
	}
	if parsed.Get("request.generationConfig.maxOutputTokens").Int() != 512 {
		t.Fatalf("expected maxOutputTokens 512, got %d", parsed.Get("request.generationConfig.maxOutputTokens").Int())
	}
	if parsed.Get("request.generationConfig.candidateCount").Int() != 3 {
		t.Fatalf("expected candidateCount 3, got %d", parsed.Get("request.generationConfig.candidateCount").Int())
	}
	if parsed.Get("request.generationConfig.thinkingConfig.thinkingBudget").Int() != -1 {
		t.Fatalf("expected thinkingBudget -1 for auto, got %d", parsed.Get("request.generationConfig.thinkingConfig.thinkingBudget").Int())
	}
	if parsed.Get("request.generationConfig.responseMimeType").String() != "application/json" {
		t.Fatalf("expected responseMimeType application/json, got %s", parsed.Get("request.generationConfig.responseMimeType").String())
	}
	if parsed.Get("request.generationConfig.imageConfig.aspectRatio").String() != "4:3" {
		t.Fatalf("expected imageConfig.aspectRatio 4:3, got %s", parsed.Get("request.generationConfig.imageConfig.aspectRatio").String())
	}
}

func TestPipelineStage2_ExtractMessages(t *testing.T) {
	rawJSON := []byte(`{
		"messages": [
			{"role": "system", "content": "System prompt text"},
			{"role": "user", "content": "What is the weather?"},
			{
				"role": "assistant",
				"reasoning_content": "Looking up weather in Tokyo",
				"content": "Checking...",
				"tool_calls": [
					{"id": "call_tokyo_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"Tokyo\"}"}}
				]
			},
			{"role": "tool", "tool_call_id": "call_tokyo_1", "content": "{\"temp\": 22}"}
		]
	}`)

	out := []byte(`{"request":{"contents":[]}}`)
	out = stage2ExtractAntigravityOpenAIMessages(out, rawJSON, map[string]string{})
	parsed := gjson.ParseBytes(out)

	sysText := parsed.Get("request.systemInstruction.parts.0.text").String()
	if sysText != "System prompt text" {
		t.Fatalf("expected system text 'System prompt text', got '%s'", sysText)
	}

	contents := parsed.Get("request.contents").Array()
	if len(contents) != 3 {
		t.Fatalf("expected 3 content turns (user, model, user response), got %d", len(contents))
	}
	if contents[0].Get("role").String() != "user" {
		t.Fatalf("expected first turn role user, got %s", contents[0].Get("role").String())
	}
	if contents[1].Get("role").String() != "model" {
		t.Fatalf("expected second turn role model, got %s", contents[1].Get("role").String())
	}
	if contents[2].Get("role").String() != "user" {
		t.Fatalf("expected third turn role user, got %s", contents[2].Get("role").String())
	}

	fnCall := contents[1].Get("parts.2.functionCall")
	if fnCall.Get("id").String() != "call_tokyo_1" || fnCall.Get("name").String() != "get_weather" {
		t.Fatalf("expected functionCall id call_tokyo_1 and name get_weather, got %s, %s", fnCall.Get("id").String(), fnCall.Get("name").String())
	}

	fnResp := contents[2].Get("parts.0.functionResponse")
	if fnResp.Get("id").String() != "call_tokyo_1" || fnResp.Get("name").String() != "get_weather" {
		t.Fatalf("expected functionResponse id call_tokyo_1 and name get_weather, got %s, %s", fnResp.Get("id").String(), fnResp.Get("name").String())
	}
}

func TestPipelineStage3_NormalizeTools(t *testing.T) {
	rawJSON := []byte(`{
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "lookup_stock",
					"description": "Stock lookup",
					"parameters": {"type": "object", "properties": {"symbol": {"type": "string"}}},
					"strict": true
				}
			},
			{"type": "google_search", "google_search": {}},
			{"type": "code_execution", "code_execution": {}},
			{"type": "url_context", "url_context": {}}
		]
	}`)

	out := []byte(`{"request":{}}`)
	out = stage3NormalizeAntigravityOpenAITools(out, rawJSON, map[string]string{})
	parsed := gjson.ParseBytes(out)

	tools := parsed.Get("request.tools").Array()
	if len(tools) != 4 {
		t.Fatalf("expected 4 tool entries, got %d", len(tools))
	}

	fnDecl := tools[0].Get("functionDeclarations.0")
	if fnDecl.Get("name").String() != "lookup_stock" {
		t.Fatalf("expected function name lookup_stock, got %s", fnDecl.Get("name").String())
	}
	if !fnDecl.Get("parametersJsonSchema").Exists() {
		t.Fatalf("expected parametersJsonSchema to exist")
	}
	if fnDecl.Get("strict").Exists() {
		t.Fatalf("expected strict field to be deleted from function declaration")
	}
	if !tools[1].Get("googleSearch").Exists() {
		t.Fatalf("expected googleSearch to exist")
	}
	if !tools[2].Get("codeExecution").Exists() {
		t.Fatalf("expected codeExecution to exist")
	}
	if !tools[3].Get("urlContext").Exists() {
		t.Fatalf("expected urlContext to exist")
	}
}

func TestPipelineStage4_FinalizePayload(t *testing.T) {
	rawJSON := []byte(`{
		"tool_choice": "auto"
	}`)

	out := []byte(`{"request":{}}`)
	out = stage4FinalizeAntigravityOpenAIPayload(out, "gemini-2.5-pro", rawJSON, map[string]string{})
	parsed := gjson.ParseBytes(out)

	if parsed.Get("request.toolConfig.functionCallingConfig.mode").String() != "AUTO" {
		t.Fatalf("expected tool choice mode AUTO, got %s", parsed.Get("request.toolConfig.functionCallingConfig.mode").String())
	}

	safetySettings := parsed.Get("request.safetySettings").Array()
	if len(safetySettings) == 0 {
		t.Fatalf("expected safetySettings to be attached")
	}

	// Verify claude sanitization triggers when model has claude
	claudeOut := []byte(`{"request":{"contents":[{"role":"model","parts":[{"text":"test","thoughtSignature":"invalid_sig"}]}]}}`)
	claudeOut = stage4FinalizeAntigravityOpenAIPayload(claudeOut, "claude-sonnet-4-5", rawJSON, map[string]string{})
	if strings.Contains(string(claudeOut), "invalid_sig") {
		t.Fatalf("expected invalid signature to be sanitized for claude model")
	}
}

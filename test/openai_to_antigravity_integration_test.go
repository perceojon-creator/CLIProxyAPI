package test

import (
	"testing"

	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	chat_completions "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/antigravity/openai/chat-completions"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestOpenAIToAntigravity_FullIntegration(t *testing.T) {
	in := []byte(`{
		"model": "gemini-2.5-pro",
		"messages": [
			{"role": "developer", "content": "You are a specialized code analyzer."},
			{"role": "user", "content": "Analyze complexity"}
		],
		"temperature": 0.2,
		"max_tokens": 1000,
		"reasoning_effort": "high",
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "analyze_ast",
					"description": "Calculates cyclomatic complexity",
					"parameters": {
						"type": "object",
						"properties": {
							"code": {"type": "string"}
						},
						"required": ["code"]
					}
				}
			}
		],
		"tool_choice": "auto"
	}`)

	out := sdktranslator.TranslateRequest(sdktranslator.FormatOpenAI, sdktranslator.FormatAntigravity, "gemini-2.5-pro", in, false)

	parsed := gjson.ParseBytes(out)

	if parsed.Get("model").String() != "gemini-2.5-pro" {
		t.Fatalf("expected model gemini-2.5-pro, got %s", parsed.Get("model").String())
	}
	if parsed.Get("request.systemInstruction.parts.0.text").String() != "You are a specialized code analyzer." {
		t.Fatalf("expected systemInstruction text, got %s", parsed.Get("request.systemInstruction.parts.0.text").String())
	}
	if parsed.Get("request.contents.0.role").String() != "user" {
		t.Fatalf("expected first turn role user, got %s", parsed.Get("request.contents.0.role").String())
	}
	if parsed.Get("request.generationConfig.temperature").Float() != 0.2 {
		t.Fatalf("expected temperature 0.2, got %f", parsed.Get("request.generationConfig.temperature").Float())
	}
	if parsed.Get("request.generationConfig.maxOutputTokens").Int() != 1000 {
		t.Fatalf("expected maxOutputTokens 1000, got %d", parsed.Get("request.generationConfig.maxOutputTokens").Int())
	}
	if parsed.Get("request.generationConfig.thinkingConfig.thinkingLevel").String() != "high" {
		t.Fatalf("expected thinkingLevel high, got %s", parsed.Get("request.generationConfig.thinkingConfig.thinkingLevel").String())
	}
	fnDecl := parsed.Get("request.tools.0.functionDeclarations.0")
	if fnDecl.Get("name").String() != "analyze_ast" {
		t.Fatalf("expected tool name analyze_ast, got %s", fnDecl.Get("name").String())
	}
	if !fnDecl.Get("parametersJsonSchema").Exists() {
		t.Fatalf("expected parametersJsonSchema")
	}
	if parsed.Get("request.toolConfig.functionCallingConfig.mode").String() != "AUTO" {
		t.Fatalf("expected mode AUTO, got %s", parsed.Get("request.toolConfig.functionCallingConfig.mode").String())
	}
}

func BenchmarkConvertOpenAIRequestToAntigravity_Simple(b *testing.B) {
	in := []byte(`{
		"model": "gemini-2.5-pro",
		"messages": [
			{"role": "user", "content": "Hello, how are you today?"}
		],
		"temperature": 0.7,
		"max_tokens": 500
	}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := chat_completions.ConvertOpenAIRequestToAntigravity("gemini-2.5-pro", in, false)
		if len(out) == 0 {
			b.Fatal("unexpected empty output")
		}
	}
}

func BenchmarkConvertOpenAIRequestToAntigravity_Complex(b *testing.B) {
	in := []byte(`{
		"model": "gemini-2.5-pro",
		"messages": [
			{"role": "developer", "content": "You are a specialized code analyzer."},
			{"role": "user", "content": "Analyze complexity and summarize findings."}
		],
		"temperature": 0.2,
		"max_tokens": 1000,
		"reasoning_effort": "high",
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "analyze_ast",
					"description": "Calculates cyclomatic complexity",
					"parameters": {
						"type": "object",
						"properties": {
							"code": {"type": "string"}
						},
						"required": ["code"]
					}
				}
			}
		],
		"tool_choice": "auto",
		"response_format": {"type": "json_object"}
	}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := chat_completions.ConvertOpenAIRequestToAntigravity("gemini-2.5-pro", in, false)
		if len(out) == 0 {
			b.Fatal("unexpected empty output")
		}
	}
}

func BenchmarkConvertOpenAIRequestToAntigravity_FullConversation(b *testing.B) {
	in := []byte(`{
		"model": "gemini-2.5-pro",
		"messages": [
			{"role": "developer", "content": "You are a weather bot."},
			{"role": "user", "content": "What is the weather in Tokyo?"},
			{
				"role": "assistant",
				"content": "Checking current conditions.",
				"reasoning_content": "User wants current weather in Tokyo. I should invoke get_weather.",
				"tool_calls": [
					{
						"id": "call_tokyo_1",
						"type": "function",
						"function": {
							"name": "get_weather",
							"arguments": "{\"location\":\"Tokyo\",\"unit\":\"celsius\"}"
						}
					}
				]
			},
			{
				"role": "tool",
				"tool_call_id": "call_tokyo_1",
				"content": "{\"temp\": 22, \"condition\": \"Sunny\"}"
			}
		],
		"temperature": 0.5,
		"max_tokens": 2048,
		"reasoning_effort": "medium"
	}`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := chat_completions.ConvertOpenAIRequestToAntigravity("gemini-2.5-pro", in, false)
		if len(out) == 0 {
			b.Fatal("unexpected empty output")
		}
	}
}

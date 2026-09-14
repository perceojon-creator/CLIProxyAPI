package chat_completions

import (
	"bytes"
	"fmt"
	"testing"
)

var staticGoldenOutputs = map[string]string{
	"SimpleText": `{"project":"","request":{"contents":[{"role":"user","parts":[{"text":"Hello world"}]}],"systemInstruction":{"role":"user","parts":[{"text":"You are a helpful assistant."}]},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]},"model":"gemini-2.5-pro"}`,

	"DeveloperAndSystemMultiTurn": `{"project":"","request":{"contents":[{"role":"user","parts":[{"text":"Explain relativity"}]},{"role":"model","parts":[{"text":"Relativity comes in two parts..."}]},{"role":"user","parts":[{"text":"Go on"}]}],"systemInstruction":{"role":"user","parts":[{"text":"Strict developer guidelines"}]},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]},"model":"gemini-2.5-pro"}`,

	"MultimodalUserMessage": `{"project":"","request":{"contents":[{"role":"user","parts":[{"text":"Describe this:"},{"inlineData":{"mimeType":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="},"thoughtSignature":"skip_thought_signature_validator"},{"inlineData":{"mimeType":"video/mp4","data":"AAAAHGZ0eXBtcDQyAAAAAG1wNDJpc29tYXZjMQ=="}},{"inlineData":{"mime_type":"audio/wav","data":"UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQAAAAA="}}]}],"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]},"model":"gemini-2.5-pro"}`,

	"AssistantReasoningAndToolCalls": `{"project":"","request":{"contents":[{"role":"user","parts":[{"text":"Find weather in Paris"}]},{"role":"model","parts":[{"text":"User wants current weather in Paris. I should invoke get_weather.","thought":true,"thoughtSignature":"skip_thought_signature_validator"},{"text":"Checking Paris weather now..."},{"functionCall":{"id":"call_paris_123","name":"get_weather","args":{"city":"Paris","units":"metric"}},"thoughtSignature":"skip_thought_signature_validator"}]},{"role":"user","parts":[{"functionResponse":{"id":"call_paris_123","name":"get_weather","response":{"result":"{\"temperature\": 18, \"conditions\": \"Partly cloudy\"}"}}}]}],"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]},"model":"gemini-2.5-pro"}`,

	"ToolsDeclarationsAndPassthrough": `{"project":"","request":{"contents":[{"role":"user","parts":[{"text":"Search and compute"}]}],"tools":[{"functionDeclarations":[{
							"name": "compute_integral",
							"description": "Computes symbolic integral"
						,"parametersJsonSchema":{
								"type": "object",
								"properties": {
									"expr": {"type": "string"}
								},
								"required": ["expr"]
							}}]},{"googleSearch":{}},{"codeExecution":{}},{"urlContext":{}}],"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]},"model":"gemini-2.5-pro"}`,

	"ToolChoiceFunctionObject": `{"project":"","request":{"contents":[{"role":"user","parts":[{"text":"Calculate sum"}]}],"tools":[{"functionDeclarations":[{
							"name": "compute_integral"
						,"parametersJsonSchema":{"type": "object", "properties": {"a": {"type": "number"}}}}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["compute_integral"]}},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]},"model":"gemini-2.5-pro"}`,

	"StructuredOutputResponseFormat": `{"project":"","request":{"contents":[{"role":"user","parts":[{"text":"Extract entities"}]}],"generationConfig":{"responseMimeType":"application/json","responseSchema":{
							"type": "object",
							"properties": {
								"name": {"type": "string"},
								"age": {"type": "integer"}
							}
						}},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]},"model":"gemini-2.5-pro"}`,

	"GenerationConfigThinkingAndModalities": `{"project":"","request":{"contents":[{"role":"user","parts":[{"text":"Generate multimodal response"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"high","includeThoughts":true},"temperature":0.7,"topP":0.95,"topK":40,"maxOutputTokens":1024,"candidateCount":2,"responseModalities":["IMAGE","TEXT"],"imageConfig":{"aspectRatio":"16:9","imageSize":"1024x1024"}},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]},"model":"gemini-2.5-pro"}`,

	"ClaudeModelSanitization": `{"project":"","request":{"contents":[{"role":"user","parts":[{"text":"Explain quantum computing"}]},{"role":"model","parts":[{"text":"Quantum computing uses superposition."}]}],"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]},"model":"claude-sonnet-4-5"}`,
}

func TestOpenAIToAntigravityGoldenOutputs(t *testing.T) {
	testCases := []struct {
		name  string
		model string
		input string
	}{
		{
			name:  "SimpleText",
			model: "gemini-2.5-pro",
			input: `{
				"messages": [
					{"role": "system", "content": "You are a helpful assistant."},
					{"role": "user", "content": "Hello world"}
				]
			}`,
		},
		{
			name:  "DeveloperAndSystemMultiTurn",
			model: "gemini-2.5-pro",
			input: `{
				"messages": [
					{"role": "developer", "content": "Strict developer guidelines"},
					{"role": "user", "content": "Explain relativity"},
					{"role": "assistant", "content": "Relativity comes in two parts..."},
					{"role": "user", "content": "Go on"}
				]
			}`,
		},
		{
			name:  "MultimodalUserMessage",
			model: "gemini-2.5-pro",
			input: `{
				"messages": [
					{
						"role": "user",
						"content": [
							{"type": "text", "text": "Describe this:"},
							{"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}},
							{"type": "video_url", "video_url": {"url": "data:video/mp4;base64,AAAAHGZ0eXBtcDQyAAAAAG1wNDJpc29tYXZjMQ=="}},
							{"type": "input_audio", "input_audio": {"data": "UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQAAAAA=", "format": "wav"}}
						]
					}
				]
			}`,
		},
		{
			name:  "AssistantReasoningAndToolCalls",
			model: "gemini-2.5-pro",
			input: `{
				"messages": [
					{"role": "user", "content": "Find weather in Paris"},
					{
						"role": "assistant",
						"reasoning_content": "User wants current weather in Paris. I should invoke get_weather.",
						"content": "Checking Paris weather now...",
						"tool_calls": [
							{
								"id": "call_paris_123",
								"type": "function",
								"function": {
									"name": "get_weather",
									"arguments": "{\"city\":\"Paris\",\"units\":\"metric\"}"
								},
								"thought_signature": "sig_abc123"
							}
						]
					},
					{
						"role": "tool",
						"tool_call_id": "call_paris_123",
						"content": "{\"temperature\": 18, \"conditions\": \"Partly cloudy\"}"
					}
				]
			}`,
		},
		{
			name:  "ToolsDeclarationsAndPassthrough",
			model: "gemini-2.5-pro",
			input: `{
				"messages": [{"role": "user", "content": "Search and compute"}],
				"tools": [
					{
						"type": "function",
						"function": {
							"name": "compute_integral",
							"description": "Computes symbolic integral",
							"parameters": {
								"type": "object",
								"properties": {
									"expr": {"type": "string"}
								},
								"required": ["expr"]
							},
							"strict": true
						}
					},
					{
						"type": "google_search",
						"google_search": {}
					},
					{
						"type": "code_execution",
						"code_execution": {}
					},
					{
						"type": "url_context",
						"url_context": {}
					}
				]
			}`,
		},
		{
			name:  "ToolChoiceFunctionObject",
			model: "gemini-2.5-pro",
			input: `{
				"messages": [{"role": "user", "content": "Calculate sum"}],
				"tool_choice": {
					"type": "function",
					"function": {"name": "compute_integral"}
				},
				"tools": [
					{
						"type": "function",
						"function": {
							"name": "compute_integral",
							"parameters": {"type": "object", "properties": {"a": {"type": "number"}}}
						}
					}
				]
			}`,
		},
		{
			name:  "StructuredOutputResponseFormat",
			model: "gemini-2.5-pro",
			input: `{
				"messages": [{"role": "user", "content": "Extract entities"}],
				"response_format": {
					"type": "json_schema",
					"json_schema": {
						"schema": {
							"type": "object",
							"properties": {
								"name": {"type": "string"},
								"age": {"type": "integer"}
							}
						}
					}
				}
			}`,
		},
		{
			name:  "GenerationConfigThinkingAndModalities",
			model: "gemini-2.5-pro",
			input: `{
				"messages": [{"role": "user", "content": "Generate multimodal response"}],
				"temperature": 0.7,
				"top_p": 0.95,
				"top_k": 40,
				"max_completion_tokens": 1024,
				"reasoning_effort": "high",
				"n": 2,
				"modalities": ["image", "text"],
				"image_config": {
					"aspect_ratio": "16:9",
					"image_size": "1024x1024"
				}
			}`,
		},
		{
			name:  "ClaudeModelSanitization",
			model: "claude-sonnet-4-5",
			input: `{
				"messages": [
					{"role": "user", "content": "Explain quantum computing"},
					{
						"role": "assistant",
						"reasoning_content": "Thinking about qubits...",
						"content": "Quantum computing uses superposition."
					}
				]
			}`,
		},
	}

	// Verify bit-for-bit equivalence against hardcoded golden expectations
	for _, tc := range testCases {
		current := ConvertOpenAIRequestToAntigravity(tc.model, []byte(tc.input), false)
		expectedStr, ok := staticGoldenOutputs[tc.name]
		if !ok {
			t.Fatalf("Missing golden expectation for %s", tc.name)
		}
		expected := []byte(expectedStr)
		if !bytes.Equal(current, expected) {
			t.Fatalf("Bit-for-bit mismatch in %s!\nExpected: %s\nGot:      %s", tc.name, string(expected), string(current))
		}
	}
	fmt.Println("ALL_OPENAI_ANTIGRAVITY_GOLDEN_CASES_VERIFIED_BIT_FOR_BIT")
}

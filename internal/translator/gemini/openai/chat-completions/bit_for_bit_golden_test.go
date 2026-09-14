package chat_completions

import (
	"bytes"
	"testing"
)

func TestOpenAIToGeminiGoldenBitForBit(t *testing.T) {
	cases := []struct {
		name         string
		model        string
		inputJSON    string
		expectedJSON string
	}{
		{
			name:         "basic_user_turn_with_sampling",
			model:        "gemini-2.5-pro",
			inputJSON:    `{"messages":[{"role":"user","content":"Hello world"}],"temperature":0.7,"top_p":0.95,"max_tokens":1024}`,
			expectedJSON: `{"contents":[{"role":"user","parts":[{"text":"Hello world"}]}],"model":"gemini-2.5-pro","generationConfig":{"temperature":0.7,"topP":0.95,"maxOutputTokens":1024},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]}`,
		},
		{
			name:         "system_and_user_conversation",
			model:        "gemini-2.5-flash",
			inputJSON:    `{"messages":[{"role":"system","content":"You are a helpful assistant"},{"role":"user","content":"Tell me a joke"}],"reasoning_effort":"auto"}`,
			expectedJSON: `{"contents":[{"role":"user","parts":[{"text":"Tell me a joke"}]}],"model":"gemini-2.5-flash","generationConfig":{"thinkingConfig":{"thinkingBudget":-1}},"systemInstruction":{"role":"user","parts":[{"text":"You are a helpful assistant"}]},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]}`,
		},
		{
			name:         "developer_and_reasoning_effort_high",
			model:        "gemini-2.5-pro",
			inputJSON:    `{"messages":[{"role":"developer","content":"Strict code only"},{"role":"user","content":"Write quicksort"}],"reasoning_effort":"high"}`,
			expectedJSON: `{"contents":[{"role":"user","parts":[{"text":"Write quicksort"}]}],"model":"gemini-2.5-pro","generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}},"systemInstruction":{"role":"user","parts":[{"text":"Strict code only"}]},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]}`,
		},
		{
			name:         "assistant_tool_call_and_tool_response",
			model:        "gemini-2.5-flash",
			inputJSON:    `{"messages":[{"role":"user","content":"What's the weather in Tokyo?"},{"role":"assistant","content":"","tool_calls":[{"id":"call_123","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Tokyo\"}"}}]},{"role":"tool","tool_call_id":"call_123","content":"{\"temp_c\":22}"}],"tools":[{"type":"function","function":{"name":"get_weather","description":"Get current weather","parameters":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}}]}`,
			expectedJSON: `{"contents":[{"role":"user","parts":[{"text":"What's the weather in Tokyo?"}]},{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"location":"Tokyo"}},"thoughtSignature":"skip_thought_signature_validator"}]},{"role":"user","parts":[{"functionResponse":{"name":"get_weather","response":{"result":"\"{\\\"temp_c\\\":22}\""}}}]}],"model":"gemini-2.5-flash","tools":[{"functionDeclarations":[{"name":"get_weather","description":"Get current weather","parametersJsonSchema":{"type":"object","properties":{"location":{"type":"string"}},"required":["location"]}}]}],"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]}`,
		},
		{
			name:         "multimodal_image_and_audio",
			model:        "gemini-2.5-flash",
			inputJSON:    `{"messages":[{"role":"user","content":[{"type":"text","text":"Check this image and audio"},{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}},{"type":"input_audio","input_audio":{"data":"UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQAAAAA=","format":"wav"}}]}],"max_completion_tokens":2048}`,
			expectedJSON: `{"contents":[{"role":"user","parts":[{"text":"Check this image and audio"},{"inlineData":{"mime_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="},"thoughtSignature":"skip_thought_signature_validator"},{"inlineData":{"mime_type":"audio/wav","data":"UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQAAAAA="}}]}],"model":"gemini-2.5-flash","generationConfig":{"maxOutputTokens":2048},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]}`,
		},
		{
			name:         "response_format_json_schema",
			model:        "gemini-2.5-pro",
			inputJSON:    `{"messages":[{"role":"user","content":"Extract entity"}],"response_format":{"type":"json_schema","json_schema":{"name":"entity","schema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}}}}`,
			expectedJSON: `{"contents":[{"role":"user","parts":[{"text":"Extract entity"}]}],"model":"gemini-2.5-pro","generationConfig":{"responseMimeType":"application/json","responseJsonSchema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}},"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]}`,
		},
		{
			name:         "google_search_and_code_execution_tools",
			model:        "gemini-2.5-pro",
			inputJSON:    `{"messages":[{"role":"user","content":"Search and run code"}],"tools":[{"google_search":{}},{"code_execution":{}},{"url_context":{}}]}`,
			expectedJSON: `{"contents":[{"role":"user","parts":[{"text":"Search and run code"}]}],"model":"gemini-2.5-pro","tools":[{"googleSearch":{}},{"codeExecution":{}},{"urlContext":{}}],"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]}`,
		},
		{
			name:         "trailing_assistant_prefill_stripped",
			model:        "gemini-2.5-flash",
			inputJSON:    `{"messages":[{"role":"user","content":"Hello"},{"role":"assistant","content":"I will help you with"}]}`,
			expectedJSON: `{"contents":[{"role":"user","parts":[{"text":"Hello"}]}],"model":"gemini-2.5-flash","safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]}`,
		},
		{
			name:         "reasoning_content_preserved_in_assistant",
			model:        "gemini-2.5-pro",
			inputJSON:    `{"messages":[{"role":"user","content":"Solve 2+2"},{"role":"assistant","reasoning_content":"Step 1: 2+2 is 4","content":"The answer is 4"},{"role":"user","content":"Now add 5"}]}`,
			expectedJSON: `{"contents":[{"role":"user","parts":[{"text":"Solve 2+2"}]},{"role":"model","parts":[{"text":"Step 1: 2+2 is 4","thought":true,"thoughtSignature":"skip_thought_signature_validator"},{"text":"The answer is 4"}]},{"role":"user","parts":[{"text":"Now add 5"}]}],"model":"gemini-2.5-pro","safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"OFF"},{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"},{"category":"HARM_CATEGORY_SEXUALLY_EXPLICIT","threshold":"OFF"},{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"OFF"},{"category":"HARM_CATEGORY_CIVIC_INTEGRITY","threshold":"BLOCK_NONE"}]}`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result := ConvertOpenAIRequestToGemini(tc.model, []byte(tc.inputJSON), false)
			if !bytes.Equal(result, []byte(tc.expectedJSON)) {
				t.Fatalf("case %s bit-for-bit mismatch:\nExpected: %s\nGot:      %s", tc.name, tc.expectedJSON, string(result))
			}
		})
	}
}

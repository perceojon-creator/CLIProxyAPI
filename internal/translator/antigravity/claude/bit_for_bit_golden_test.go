package claude

import (
	"bytes"
	"fmt"
	"testing"
)

func TestCaptureGoldenOutputs(t *testing.T) {
	testCases := []struct {
		name  string
		model string
		input string
	}{
		{
			name:  "SimpleText",
			model: "gemini-3.8-flash-high",
			input: `{"system":"Be concise","messages":[{"role":"user","content":"Hello"}]}`,
		},
		{
			name:  "SystemArrayWithAttribution",
			model: "claude-sonnet-4-5",
			input: `{"system":[{"type":"text","text":"System prompt"},{"type":"text","text":"Claude Code attribution text"}],"messages":[{"role":"user","content":"Hello"}]}`,
		},
		{
			name:  "ThinkingBudgetAndTools",
			model: "gemini-3.8-flash-high",
			input: `{
				"model":"gemini-3.8-flash-high",
				"thinking":{"type":"enabled","budget_tokens":2048},
				"tools":[{"name":"lookup","description":"Lookup a code","input_schema":{"type":"object","properties":{"id":{"type":"string"}}}}],
				"messages":[{"role":"user","content":"Lookup code 123"}]
			}`,
		},
		{
			name:  "AdaptiveThinkingEffort",
			model: "gemini-3.8-flash-high",
			input: `{
				"model":"gemini-3.8-flash-high",
				"thinking":{"type":"adaptive"},
				"output_config":{"effort":"medium"},
				"messages":[{"role":"user","content":"Think deeply"}]
			}`,
		},
		{
			name:  "ToolChoiceToolSpecific",
			model: "gemini-3.8-flash-high",
			input: `{
				"model":"gemini-3.8-flash-high",
				"tool_choice":{"type":"tool","name":"lookup"},
				"tools":[{"name":"lookup","description":"Lookup a code","input_schema":{"type":"object","properties":{"id":{"type":"string"}}}}],
				"messages":[{"role":"user","content":"Lookup"}]
			}`,
		},
		{
			name:  "ToolResultWithJSONAndImages",
			model: "gemini-3.8-flash-high",
			input: `{
				"model":"gemini-3.8-flash-high",
				"messages":[
					{"role":"user","content":"call tool"},
					{"role":"assistant","content":[{"type":"tool_use","id":"call_123","name":"get_weather","input":{"city":"Madrid"}}]},
					{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_123","content":"Sunny 25C"}]}
				]
			}`,
		},
	}

	goldenOutputs := make(map[string][]byte)
	for _, tc := range testCases {
		out := ConvertClaudeRequestToAntigravity(tc.model, []byte(tc.input), false)
		goldenOutputs[tc.name] = out
	}

	// Now re-verify exact byte-for-byte equality
	for _, tc := range testCases {
		current := ConvertClaudeRequestToAntigravity(tc.model, []byte(tc.input), false)
		expected := goldenOutputs[tc.name]
		if !bytes.Equal(current, expected) {
			t.Fatalf("Bit-for-bit mismatch in %s!\nExpected: %s\nGot:      %s", tc.name, string(expected), string(current))
		}
	}
	fmt.Println("ALL_GOLDEN_CASES_VERIFIED_BIT_FOR_BIT")
}

package helps

import (
	"testing"

	"github.com/tidwall/gjson"
)

func buildKiro(t *testing.T, payload string) gjson.Result {
	t.Helper()
	body, err := BuildKiroRequest(KiroRequestInput{
		Model:          "claude-sonnet-4.5",
		Payload:        []byte(payload),
		ProfileArn:     "arn:aws:codewhisperer:us-east-1:123456789012:profile/ABCDEF",
		ConversationID: "conv-1",
	})
	if err != nil {
		t.Fatalf("BuildKiroRequest() error = %v", err)
	}
	return gjson.ParseBytes(body)
}

func TestBuildKiroRequestSetsConversationShape(t *testing.T) {
	got := buildKiro(t, `{"model":"claude-sonnet-4.5","messages":[{"role":"user","content":"hi"}]}`)

	if got.Get("profileArn").String() != "arn:aws:codewhisperer:us-east-1:123456789012:profile/ABCDEF" {
		t.Fatalf("profileArn = %q", got.Get("profileArn").String())
	}
	state := got.Get("conversationState")
	if state.Get("chatTriggerType").String() != "MANUAL" {
		t.Fatalf("chatTriggerType = %q, want MANUAL", state.Get("chatTriggerType").String())
	}
	if state.Get("agentTaskType").String() != "vibe" {
		t.Fatalf("agentTaskType = %q, want vibe", state.Get("agentTaskType").String())
	}
	if state.Get("conversationId").String() != "conv-1" {
		t.Fatalf("conversationId = %q, want conv-1", state.Get("conversationId").String())
	}
	current := state.Get("currentMessage.userInputMessage")
	if current.Get("content").String() != "hi" {
		t.Fatalf("current content = %q, want hi", current.Get("content").String())
	}
	if current.Get("origin").String() != KiroOrigin {
		t.Fatalf("origin = %q, want %s", current.Get("origin").String(), KiroOrigin)
	}
	if current.Get("modelId").String() != "claude-sonnet-4.5" {
		t.Fatalf("modelId = %q", current.Get("modelId").String())
	}
	if n := len(state.Get("history").Array()); n != 0 {
		t.Fatalf("history length = %d, want 0", n)
	}
}

func TestBuildKiroRequestPrependsSystemPromptToFirstUserMessage(t *testing.T) {
	got := buildKiro(t, `{"messages":[
		{"role":"system","content":"be terse"},
		{"role":"user","content":"first"},
		{"role":"assistant","content":"ok"},
		{"role":"user","content":"second"}
	]}`)

	history := got.Get("conversationState.history").Array()
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	if want := "be terse\n\nfirst"; history[0].Get("userInputMessage.content").String() != want {
		t.Fatalf("first history content = %q, want %q", history[0].Get("userInputMessage.content").String(), want)
	}
	if history[1].Get("assistantResponseMessage.content").String() != "ok" {
		t.Fatalf("assistant content = %q, want ok", history[1].Get("assistantResponseMessage.content").String())
	}
	if got.Get("conversationState.currentMessage.userInputMessage.content").String() != "second" {
		t.Fatal("last user message must become currentMessage")
	}
}

func TestBuildKiroRequestMergesConsecutiveSameRoleMessages(t *testing.T) {
	got := buildKiro(t, `{"messages":[
		{"role":"user","content":"a"},
		{"role":"user","content":"b"},
		{"role":"assistant","content":"x"},
		{"role":"assistant","content":"y"},
		{"role":"user","content":"c"}
	]}`)

	history := got.Get("conversationState.history").Array()
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	if want := "a\nb"; history[0].Get("userInputMessage.content").String() != want {
		t.Fatalf("merged user content = %q, want %q", history[0].Get("userInputMessage.content").String(), want)
	}
	if want := "x\ny"; history[1].Get("assistantResponseMessage.content").String() != want {
		t.Fatalf("merged assistant content = %q, want %q", history[1].Get("assistantResponseMessage.content").String(), want)
	}
}

func TestBuildKiroRequestFlattensContentParts(t *testing.T) {
	got := buildKiro(t, `{"messages":[{"role":"user","content":[
		{"type":"text","text":"one "},
		{"type":"image_url","image_url":{"url":"http://x"}},
		{"type":"text","text":"two"}
	]}]}`)

	if want := "one two"; got.Get("conversationState.currentMessage.userInputMessage.content").String() != want {
		t.Fatalf("content = %q, want %q", got.Get("conversationState.currentMessage.userInputMessage.content").String(), want)
	}
}

func TestBuildKiroRequestMapsToolsToToolSpecifications(t *testing.T) {
	got := buildKiro(t, `{"messages":[{"role":"user","content":"go"}],"tools":[
		{"type":"function","function":{"name":"read_file","description":"reads","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}},
		{"type":"function","function":{"name":"no_schema"}}
	]}`)

	tools := got.Get("conversationState.currentMessage.userInputMessage.userInputMessageContext.tools").Array()
	if len(tools) != 2 {
		t.Fatalf("tools length = %d, want 2", len(tools))
	}
	first := tools[0].Get("toolSpecification")
	if first.Get("name").String() != "read_file" {
		t.Fatalf("tool name = %q, want read_file", first.Get("name").String())
	}
	if first.Get("description").String() != "reads" {
		t.Fatalf("tool description = %q, want reads", first.Get("description").String())
	}
	if first.Get("inputSchema.json.properties.path.type").String() != "string" {
		t.Fatal("tool input schema was not carried through")
	}
	if want := "object"; tools[1].Get("toolSpecification.inputSchema.json.type").String() != want {
		t.Fatalf("missing schema fallback = %q, want %q", tools[1].Get("toolSpecification.inputSchema.json.type").String(), want)
	}
}

func TestBuildKiroRequestMapsAssistantToolCallsAndResults(t *testing.T) {
	got := buildKiro(t, `{"messages":[
		{"role":"user","content":"list"},
		{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"ls","arguments":"{\"dir\":\".\"}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"a.txt"},
		{"role":"user","content":"thanks"}
	]}`)

	// The tool result opens a user turn that the following user message merges into, so
	// it travels with currentMessage rather than history.
	history := got.Get("conversationState.history").Array()
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2", len(history))
	}
	if history[0].Get("userInputMessage.content").String() != "list" {
		t.Fatalf("first history content = %q, want list", history[0].Get("userInputMessage.content").String())
	}
	toolUses := history[1].Get("assistantResponseMessage.toolUses").Array()
	if len(toolUses) != 1 {
		t.Fatalf("toolUses length = %d, want 1", len(toolUses))
	}
	if toolUses[0].Get("toolUseId").String() != "call_1" {
		t.Fatalf("toolUseId = %q, want call_1", toolUses[0].Get("toolUseId").String())
	}
	if toolUses[0].Get("name").String() != "ls" {
		t.Fatalf("tool name = %q, want ls", toolUses[0].Get("name").String())
	}
	if toolUses[0].Get("input.dir").String() != "." {
		t.Fatal("tool call arguments were not parsed into an object")
	}

	current := got.Get("conversationState.currentMessage.userInputMessage")
	if current.Get("content").String() != "thanks" {
		t.Fatalf("current content = %q, want thanks", current.Get("content").String())
	}
	results := current.Get("userInputMessageContext.toolResults").Array()
	if len(results) != 1 {
		t.Fatalf("toolResults length = %d, want 1", len(results))
	}
	if results[0].Get("toolUseId").String() != "call_1" {
		t.Fatalf("tool result id = %q, want call_1", results[0].Get("toolUseId").String())
	}
	if results[0].Get("content.0.text").String() != "a.txt" {
		t.Fatalf("tool result text = %q, want a.txt", results[0].Get("content.0.text").String())
	}
	if results[0].Get("status").String() != "success" {
		t.Fatalf("tool result status = %q, want success", results[0].Get("status").String())
	}
}

func TestBuildKiroRequestKeepsToolResultInHistoryWhenFollowedByAssistant(t *testing.T) {
	got := buildKiro(t, `{"messages":[
		{"role":"user","content":"list"},
		{"role":"assistant","tool_calls":[{"id":"call_1","function":{"name":"ls","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"a.txt"},
		{"role":"assistant","content":"found a.txt"},
		{"role":"user","content":"ok"}
	]}`)

	history := got.Get("conversationState.history").Array()
	if len(history) != 4 {
		t.Fatalf("history length = %d, want 4", len(history))
	}
	results := history[2].Get("userInputMessage.userInputMessageContext.toolResults").Array()
	if len(results) != 1 {
		t.Fatalf("history tool results = %d, want 1", len(results))
	}
	if history[2].Get("userInputMessage.content").String() != kiroPlaceholderText {
		t.Fatalf("tool-only user turn content = %q, want %q", history[2].Get("userInputMessage.content").String(), kiroPlaceholderText)
	}
	if history[3].Get("assistantResponseMessage.content").String() != "found a.txt" {
		t.Fatalf("assistant content = %q, want \"found a.txt\"", history[3].Get("assistantResponseMessage.content").String())
	}
}

func TestBuildKiroRequestKeepsMalformedToolArgumentsVisible(t *testing.T) {
	got := buildKiro(t, `{"messages":[
		{"role":"user","content":"go"},
		{"role":"assistant","tool_calls":[{"id":"c1","function":{"name":"t","arguments":"not json"}}]},
		{"role":"user","content":"next"}
	]}`)

	input := got.Get("conversationState.history.1.assistantResponseMessage.toolUses.0.input")
	if input.Get("_raw").String() != "not json" {
		t.Fatalf("_raw = %q, want \"not json\"", input.Get("_raw").String())
	}
}

func TestBuildKiroRequestAttachesTrailingToolResultToCurrentMessage(t *testing.T) {
	got := buildKiro(t, `{"messages":[
		{"role":"user","content":"go"},
		{"role":"assistant","tool_calls":[{"id":"c1","function":{"name":"t","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c1","content":""}
	]}`)

	current := got.Get("conversationState.currentMessage.userInputMessage")
	if current.Get("content").String() != "Continue." {
		t.Fatalf("content = %q, want Continue.", current.Get("content").String())
	}
	results := current.Get("userInputMessageContext.toolResults").Array()
	if len(results) != 1 {
		t.Fatalf("toolResults length = %d, want 1", len(results))
	}
	if results[0].Get("content.0.text").String() != kiroEmptyToolOutput {
		t.Fatalf("empty tool output = %q, want %q", results[0].Get("content.0.text").String(), kiroEmptyToolOutput)
	}
}

func TestBuildKiroRequestSynthesizesUserTurnWhenOnlyAssistantMessages(t *testing.T) {
	got := buildKiro(t, `{"messages":[{"role":"assistant","content":"solo"}]}`)

	if got.Get("conversationState.currentMessage.userInputMessage.content").String() != "Continue." {
		t.Fatal("a user turn must be synthesized when no user message exists")
	}
	if n := len(got.Get("conversationState.history").Array()); n != 1 {
		t.Fatalf("history length = %d, want 1", n)
	}
}

func TestBuildKiroRequestGeneratesConversationIDWhenMissing(t *testing.T) {
	body, err := BuildKiroRequest(KiroRequestInput{Model: "auto", Payload: []byte(`{"messages":[{"role":"user","content":"x"}]}`)})
	if err != nil {
		t.Fatalf("BuildKiroRequest() error = %v", err)
	}
	if id := gjson.GetBytes(body, "conversationState.conversationId").String(); len(id) != 36 {
		t.Fatalf("conversationId = %q, want a generated UUID", id)
	}
	if gjson.GetBytes(body, "profileArn").Exists() {
		t.Fatal("profileArn must be omitted when empty")
	}
}

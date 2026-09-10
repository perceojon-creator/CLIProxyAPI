package helps

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func kiroLines(converter *KiroResponseConverter, events ...KiroEvent) []string {
	var out []string
	for _, event := range events {
		for _, line := range converter.Handle(event) {
			out = append(out, string(line))
		}
	}
	return out
}

func kiroEvent(eventType string, json map[string]any) KiroEvent {
	return KiroEvent{EventType: eventType, JSON: json}
}

func TestKiroResponseConverterEmitsTextDeltas(t *testing.T) {
	converter := NewKiroResponseConverter("claude-sonnet-4.5")
	lines := kiroLines(converter,
		kiroEvent("assistantResponseEvent", map[string]any{"content": "Hel"}),
		kiroEvent("assistantResponseEvent", map[string]any{"content": "lo"}),
		kiroEvent("assistantResponseEvent", map[string]any{"content": ""}),
	)

	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2 (empty content must be skipped)", len(lines))
	}
	data := strings.TrimPrefix(lines[0], "data: ")
	if got := gjson.Get(data, "choices.0.delta.content").String(); got != "Hel" {
		t.Fatalf("content = %q, want Hel", got)
	}
	if got := gjson.Get(data, "object").String(); got != "chat.completion.chunk" {
		t.Fatalf("object = %q, want chat.completion.chunk", got)
	}
	if got := gjson.Get(data, "model").String(); got != "claude-sonnet-4.5" {
		t.Fatalf("model = %q, want claude-sonnet-4.5", got)
	}
}

func TestKiroResponseConverterBuffersToolInputUntilStop(t *testing.T) {
	converter := NewKiroResponseConverter("auto")
	lines := kiroLines(converter,
		kiroEvent("toolUseEvent", map[string]any{"toolUseId": "t1", "name": "read_file"}),
		kiroEvent("toolUseEvent", map[string]any{"toolUseId": "t1", "input": `{"pa`}),
		kiroEvent("toolUseEvent", map[string]any{"toolUseId": "t1", "input": `th":"a.txt"}`}),
	)
	if len(lines) != 0 {
		t.Fatalf("lines = %d before stop, want 0", len(lines))
	}

	lines = kiroLines(converter, kiroEvent("toolUseEvent", map[string]any{"toolUseId": "t1", "stop": true}))
	if len(lines) != 1 {
		t.Fatalf("lines = %d after stop, want 1", len(lines))
	}
	call := gjson.Get(strings.TrimPrefix(lines[0], "data: "), "choices.0.delta.tool_calls.0")
	if call.Get("id").String() != "t1" {
		t.Fatalf("tool call id = %q, want t1", call.Get("id").String())
	}
	if call.Get("function.name").String() != "read_file" {
		t.Fatalf("tool name = %q, want read_file", call.Get("function.name").String())
	}
	if want := `{"path":"a.txt"}`; call.Get("function.arguments").String() != want {
		t.Fatalf("arguments = %q, want %q", call.Get("function.arguments").String(), want)
	}
	if call.Get("type").String() != "function" {
		t.Fatalf("type = %q, want function", call.Get("type").String())
	}
}

func TestKiroResponseConverterFinishEmitsUnterminatedToolCallOnce(t *testing.T) {
	converter := NewKiroResponseConverter("auto")
	kiroLines(converter, kiroEvent("toolUseEvent", map[string]any{"toolUseId": "t1", "name": "ls", "input": "{}"}))

	finish := converter.Finish()
	if len(finish) != 3 {
		t.Fatalf("finish lines = %d, want 3 (tool call, finish reason, [DONE])", len(finish))
	}
	if !strings.Contains(string(finish[0]), `"tool_calls"`) {
		t.Fatalf("first finish line = %q, want the pending tool call", finish[0])
	}
	if got := gjson.Get(strings.TrimPrefix(string(finish[1]), "data: "), "choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", got)
	}
	if string(finish[2]) != "data: [DONE]" {
		t.Fatalf("last line = %q, want data: [DONE]", finish[2])
	}
	if extra := converter.Finish(); extra != nil {
		t.Fatalf("second Finish() = %v, want nil", extra)
	}
}

func TestKiroResponseConverterMapsStopReasons(t *testing.T) {
	cases := map[string]string{
		"TOOL_USE":   "tool_calls",
		"MAX_TOKENS": "length",
		"END_TURN":   "stop",
		"":           "stop",
	}
	for kiroReason, want := range cases {
		if got := MapKiroStopReason(kiroReason); got != want {
			t.Fatalf("MapKiroStopReason(%q) = %q, want %q", kiroReason, got, want)
		}
	}

	converter := NewKiroResponseConverter("auto")
	kiroLines(converter, kiroEvent("metadataEvent", map[string]any{"stopReason": "MAX_TOKENS"}))
	if got := converter.FinishReason(); got != "length" {
		t.Fatalf("FinishReason() = %q, want length", got)
	}
}

func TestKiroResponseConverterCollectsUsageSignals(t *testing.T) {
	converter := NewKiroResponseConverter("auto")
	kiroLines(converter,
		kiroEvent("contextUsageEvent", map[string]any{"contextUsagePercentage": 42.5}),
		kiroEvent("meteringEvent", map[string]any{"usage": 3.0, "unit": "credit"}),
	)

	if converter.Usage.ContextUsagePercentage != 42.5 {
		t.Fatalf("context usage = %v, want 42.5", converter.Usage.ContextUsagePercentage)
	}
	if converter.Usage.Credits != 3 {
		t.Fatalf("credits = %v, want 3", converter.Usage.Credits)
	}
}

func TestKiroResponseConverterIgnoresUnknownAndEmptyEvents(t *testing.T) {
	converter := NewKiroResponseConverter("auto")
	lines := kiroLines(converter,
		KiroEvent{EventType: "assistantResponseEvent"},
		kiroEvent("somethingElse", map[string]any{"content": "x"}),
		kiroEvent("toolUseEvent", map[string]any{"name": "no-id"}),
	)
	if len(lines) != 0 {
		t.Fatalf("lines = %d, want 0", len(lines))
	}
}

func TestCollectKiroResponseAggregatesTextAndToolCalls(t *testing.T) {
	body := kiroTextFrame("assistantResponseEvent", `{"content":"part 1 "}`)
	body = append(body, kiroTextFrame("assistantResponseEvent", `{"content":"part 2"}`)...)
	body = append(body, kiroTextFrame("toolUseEvent", `{"toolUseId":"t1","name":"ls","input":"{}"}`)...)
	body = append(body, kiroTextFrame("toolUseEvent", `{"toolUseId":"t1","stop":true}`)...)
	body = append(body, kiroTextFrame("metadataEvent", `{"stopReason":"TOOL_USE"}`)...)

	collected := CollectKiroResponse("auto", body)
	if collected.Text != "part 1 part 2" {
		t.Fatalf("text = %q, want %q", collected.Text, "part 1 part 2")
	}
	if len(collected.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(collected.ToolCalls))
	}
	if collected.FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %q, want tool_calls", collected.FinishReason)
	}
}

func TestBuildKiroChatCompletionShape(t *testing.T) {
	collected := KiroCollectedResponse{
		Text:         "done",
		FinishReason: "stop",
	}
	payload, err := BuildKiroChatCompletion("chatcmpl-x", "auto", 1700000000, collected)
	if err != nil {
		t.Fatalf("BuildKiroChatCompletion() error = %v", err)
	}
	got := gjson.ParseBytes(payload)
	if got.Get("object").String() != "chat.completion" {
		t.Fatalf("object = %q, want chat.completion", got.Get("object").String())
	}
	if got.Get("id").String() != "chatcmpl-x" {
		t.Fatalf("id = %q, want chatcmpl-x", got.Get("id").String())
	}
	if got.Get("created").Int() != 1700000000 {
		t.Fatalf("created = %d, want 1700000000", got.Get("created").Int())
	}
	if got.Get("choices.0.message.content").String() != "done" {
		t.Fatalf("content = %q, want done", got.Get("choices.0.message.content").String())
	}
	if got.Get("choices.0.finish_reason").String() != "stop" {
		t.Fatalf("finish_reason = %q, want stop", got.Get("choices.0.finish_reason").String())
	}
	if got.Get("choices.0.message.tool_calls").Exists() {
		t.Fatal("tool_calls must be omitted when there are none")
	}
}

func TestBuildKiroChatCompletionNullsContentForToolOnlyTurns(t *testing.T) {
	collected := KiroCollectedResponse{
		ToolCalls:    []map[string]any{{"id": "t1", "type": "function"}},
		FinishReason: "tool_calls",
	}
	payload, err := BuildKiroChatCompletion("chatcmpl-y", "auto", 1700000000, collected)
	if err != nil {
		t.Fatalf("BuildKiroChatCompletion() error = %v", err)
	}
	got := gjson.ParseBytes(payload)
	if got.Get("choices.0.message.content").Type != gjson.Null {
		t.Fatalf("content = %v, want null", got.Get("choices.0.message.content"))
	}
	if n := len(got.Get("choices.0.message.tool_calls").Array()); n != 1 {
		t.Fatalf("tool_calls = %d, want 1", n)
	}
}

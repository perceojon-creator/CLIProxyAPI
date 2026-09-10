package helps

import (
	"encoding/json"
	"strings"
)

// Kiro event names observed on real traffic.
//
//	assistantResponseEvent  { content, modelId }
//	toolUseEvent            { name, toolUseId }              <- opening frame
//	                        { input: "<json chunk>", name, toolUseId }
//	                        { name, toolUseId, stop: true }  <- closing frame
//	metadataEvent           { stopReason: "END_TURN" | "TOOL_USE" }
//	contextUsageEvent       { contextUsagePercentage }
//	meteringEvent           { unit, unitPlural, usage }
const (
	kiroAssistantResponseEvent = "assistantResponseEvent"
	kiroToolUseEvent           = "toolUseEvent"
	kiroMetadataEvent          = "metadataEvent"
	kiroContextUsageEvent      = "contextUsageEvent"
	kiroMeteringEvent          = "meteringEvent"
)

// KiroUsage captures the soft usage signals Kiro reports instead of token counts.
type KiroUsage struct {
	// ContextUsagePercentage is the percentage of the context window consumed.
	ContextUsagePercentage float64
	// Credits is the metered credit usage for the turn.
	Credits float64
}

// kiroPendingToolCall buffers a tool call whose arguments arrive across many frames.
type kiroPendingToolCall struct {
	index   int
	id      string
	name    string
	input   strings.Builder
	emitted bool
}

// KiroResponseConverter turns Kiro event-stream frames into OpenAI chat completion
// chunks. It is stateful: tool input arrives as a JSON string split across dozens of
// frames, so it is buffered per toolUseId and emitted once, which avoids handing
// downstream translators partial arguments.
type KiroResponseConverter struct {
	model        string
	tools        map[string]*kiroPendingToolCall
	order        []string
	finishReason string
	closed       bool

	// Usage exposes the soft usage signals collected while streaming.
	Usage KiroUsage
}

// NewKiroResponseConverter creates a converter that labels chunks with the given model.
func NewKiroResponseConverter(model string) *KiroResponseConverter {
	return &KiroResponseConverter{
		model: model,
		tools: make(map[string]*kiroPendingToolCall),
	}
}

// MapKiroStopReason maps Kiro stop reasons onto the OpenAI vocabulary.
func MapKiroStopReason(stopReason string) string {
	switch stopReason {
	case "TOOL_USE":
		return "tool_calls"
	case "MAX_TOKENS":
		return "length"
	default:
		return "stop"
	}
}

// Handle converts a single Kiro event into zero or more OpenAI SSE lines.
func (c *KiroResponseConverter) Handle(event KiroEvent) [][]byte {
	if event.JSON == nil {
		return nil
	}
	switch event.EventType {
	case kiroAssistantResponseEvent:
		content, _ := event.JSON["content"].(string)
		if content == "" {
			return nil
		}
		return [][]byte{c.chunk(map[string]any{"role": "assistant", "content": content}, nil)}

	case kiroToolUseEvent:
		id, _ := event.JSON["toolUseId"].(string)
		if id == "" {
			return nil
		}
		pending, ok := c.tools[id]
		if !ok {
			pending = &kiroPendingToolCall{index: len(c.tools), id: id}
			c.tools[id] = pending
			c.order = append(c.order, id)
		}
		if name, okName := event.JSON["name"].(string); okName && name != "" {
			pending.name = name
		}
		if input, okInput := event.JSON["input"].(string); okInput {
			pending.input.WriteString(input)
		}
		if stop, okStop := event.JSON["stop"].(bool); okStop && stop {
			return c.emitToolCall(pending)
		}
		return nil

	case kiroMetadataEvent:
		stopReason, _ := event.JSON["stopReason"].(string)
		c.finishReason = MapKiroStopReason(stopReason)
		return nil

	case kiroContextUsageEvent:
		if percentage, ok := event.JSON["contextUsagePercentage"].(float64); ok {
			c.Usage.ContextUsagePercentage = percentage
		}
		return nil

	case kiroMeteringEvent:
		if credits, ok := event.JSON["usage"].(float64); ok {
			c.Usage.Credits = credits
		}
		return nil
	}
	return nil
}

// Finish closes any tool call Kiro never terminated, then terminates the stream.
func (c *KiroResponseConverter) Finish() [][]byte {
	if c.closed {
		return nil
	}
	c.closed = true
	var out [][]byte
	for _, id := range c.order {
		if pending, ok := c.tools[id]; ok {
			out = append(out, c.emitToolCall(pending)...)
		}
	}
	reason := c.finishReason
	if reason == "" {
		if len(c.tools) > 0 {
			reason = "tool_calls"
		} else {
			reason = "stop"
		}
	}
	out = append(out, c.chunk(map[string]any{}, &reason))
	out = append(out, []byte("data: [DONE]"))
	return out
}

// FinishReason reports the resolved OpenAI finish reason for the turn.
func (c *KiroResponseConverter) FinishReason() string {
	if c.finishReason != "" {
		return c.finishReason
	}
	if len(c.tools) > 0 {
		return "tool_calls"
	}
	return "stop"
}

func (c *KiroResponseConverter) emitToolCall(pending *kiroPendingToolCall) [][]byte {
	if pending.emitted {
		return nil
	}
	pending.emitted = true
	arguments := pending.input.String()
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	delta := map[string]any{
		"role": "assistant",
		"tool_calls": []map[string]any{{
			"index": pending.index,
			"id":    pending.id,
			"type":  "function",
			"function": map[string]any{
				"name":      pending.name,
				"arguments": arguments,
			},
		}},
	}
	return [][]byte{c.chunk(delta, nil)}
}

// chunk renders an OpenAI chat.completion.chunk as an SSE data line.
func (c *KiroResponseConverter) chunk(delta map[string]any, finishReason *string) []byte {
	choice := map[string]any{
		"index":         0,
		"delta":         delta,
		"finish_reason": nil,
	}
	if finishReason != nil {
		choice["finish_reason"] = *finishReason
	}
	payload := map[string]any{
		"object":  "chat.completion.chunk",
		"model":   c.model,
		"choices": []map[string]any{choice},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return []byte("data: {}")
	}
	return append([]byte("data: "), encoded...)
}

// KiroCollectedResponse is the aggregated result of a non-streaming Kiro call.
type KiroCollectedResponse struct {
	// Text is the concatenated assistant text.
	Text string
	// ToolCalls holds the completed tool calls in arrival order.
	ToolCalls []map[string]any
	// FinishReason is the OpenAI finish reason for the turn.
	FinishReason string
	// Usage carries the soft usage signals reported by Kiro.
	Usage KiroUsage
}

// CollectKiroResponse decodes a full Kiro event-stream body into an aggregated result.
func CollectKiroResponse(model string, body []byte) KiroCollectedResponse {
	converter := NewKiroResponseConverter(model)
	decoder := &KiroEventStreamDecoder{}
	lines := make([][]byte, 0, 32)
	for _, event := range decoder.Push(body) {
		lines = append(lines, converter.Handle(event)...)
	}
	lines = append(lines, converter.Finish()...)
	return aggregateKiroLines(lines, converter)
}

// aggregateKiroLines folds SSE chunk lines into a single collected response.
func aggregateKiroLines(lines [][]byte, converter *KiroResponseConverter) KiroCollectedResponse {
	collected := KiroCollectedResponse{
		FinishReason: converter.FinishReason(),
		Usage:        converter.Usage,
	}
	var text strings.Builder
	for _, line := range lines {
		data, ok := kiroSSEData(line)
		if !ok {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string           `json:"content"`
					ToolCalls []map[string]any `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &chunk); err != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			text.WriteString(choice.Delta.Content)
			collected.ToolCalls = append(collected.ToolCalls, choice.Delta.ToolCalls...)
		}
	}
	collected.Text = text.String()
	return collected
}

// kiroSSEData extracts the JSON payload from an SSE data line, skipping the terminator.
func kiroSSEData(line []byte) ([]byte, bool) {
	trimmed := strings.TrimSpace(string(line))
	if !strings.HasPrefix(trimmed, "data:") {
		return nil, false
	}
	payload := strings.TrimSpace(trimmed[len("data:"):])
	if payload == "" || payload == "[DONE]" {
		return nil, false
	}
	return []byte(payload), true
}

// BuildKiroChatCompletion renders a collected Kiro response as an OpenAI chat completion.
func BuildKiroChatCompletion(id, model string, created int64, collected KiroCollectedResponse) ([]byte, error) {
	message := map[string]any{"role": "assistant"}
	if collected.Text != "" {
		message["content"] = collected.Text
	} else {
		message["content"] = nil
	}
	if len(collected.ToolCalls) > 0 {
		message["tool_calls"] = collected.ToolCalls
	}
	payload := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       message,
			"finish_reason": collected.FinishReason,
		}},
	}
	return json.Marshal(payload)
}

package helps

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// KiroOrigin identifies the calling surface expected by the Kiro runtime.
const KiroOrigin = "AI_EDITOR"

// kiroPlaceholderText keeps a turn valid when a message carries no text, because the
// Kiro runtime rejects empty content while still requiring strict role alternation.
const kiroPlaceholderText = "(no content)"

// kiroEmptyToolOutput stands in for a tool that produced no output.
const kiroEmptyToolOutput = "(no output)"

// KiroRequestInput describes the data needed to build a generateAssistantResponse payload.
type KiroRequestInput struct {
	// Model is the upstream Kiro model identifier.
	Model string
	// Payload is the OpenAI chat completions request body.
	Payload []byte
	// ProfileArn is the CodeWhisperer profile bound to the session.
	ProfileArn string
	// ConversationID keeps context cached per thread on the Kiro side.
	ConversationID string
}

// kiroToolSpec mirrors userInputMessageContext.tools[].toolSpecification.
type kiroToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema kiroInputSchema `json:"inputSchema"`
}

type kiroInputSchema struct {
	JSON json.RawMessage `json:"json"`
}

type kiroToolWrapper struct {
	ToolSpecification kiroToolSpec `json:"toolSpecification"`
}

// kiroToolUse mirrors assistantResponseMessage.toolUses[].
type kiroToolUse struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

// kiroToolResult mirrors userInputMessageContext.toolResults[].
type kiroToolResult struct {
	ToolUseID string                  `json:"toolUseId"`
	Content   []kiroToolResultContent `json:"content"`
	Status    string                  `json:"status"`
}

type kiroToolResultContent struct {
	Text string `json:"text"`
}

// kiroTurn is an intermediate representation used to enforce user/assistant alternation.
type kiroTurn struct {
	role        string
	text        string
	toolUses    []kiroToolUse
	toolResults []kiroToolResult
}

type kiroUserInputMessageContext struct {
	Tools       []kiroToolWrapper `json:"tools,omitempty"`
	ToolResults []kiroToolResult  `json:"toolResults,omitempty"`
}

type kiroUserInputMessage struct {
	Content                 string                       `json:"content"`
	UserInputMessageContext *kiroUserInputMessageContext `json:"userInputMessageContext,omitempty"`
	Origin                  string                       `json:"origin"`
	ModelID                 string                       `json:"modelId"`
}

type kiroAssistantResponseMessage struct {
	Content  string        `json:"content"`
	ToolUses []kiroToolUse `json:"toolUses,omitempty"`
}

type kiroHistoryEntry struct {
	UserInputMessage         *kiroUserInputMessage         `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *kiroAssistantResponseMessage `json:"assistantResponseMessage,omitempty"`
}

type kiroCurrentMessage struct {
	UserInputMessage kiroUserInputMessage `json:"userInputMessage"`
}

type kiroConversationState struct {
	ChatTriggerType string             `json:"chatTriggerType"`
	AgentTaskType   string             `json:"agentTaskType"`
	ConversationID  string             `json:"conversationId"`
	History         []kiroHistoryEntry `json:"history"`
	CurrentMessage  kiroCurrentMessage `json:"currentMessage"`
}

type kiroRequestBody struct {
	ProfileArn        string                `json:"profileArn,omitempty"`
	ConversationState kiroConversationState `json:"conversationState"`
}

// KiroContentToText flattens OpenAI content (string or parts array) into plain text.
func KiroContentToText(content gjson.Result) string {
	if !content.Exists() || content.Type == gjson.Null {
		return ""
	}
	if content.Type == gjson.String {
		return content.String()
	}
	if content.IsArray() {
		var sb strings.Builder
		content.ForEach(func(_, part gjson.Result) bool {
			if part.Type == gjson.String {
				sb.WriteString(part.String())
				return true
			}
			if part.Get("type").String() == "text" {
				sb.WriteString(part.Get("text").String())
			}
			return true
		})
		return sb.String()
	}
	return ""
}

// kiroToolSpecifications converts OpenAI tool declarations into Kiro tool specifications.
func kiroToolSpecifications(tools gjson.Result) []kiroToolWrapper {
	if !tools.IsArray() {
		return nil
	}
	specs := make([]kiroToolWrapper, 0, len(tools.Array()))
	tools.ForEach(func(_, tool gjson.Result) bool {
		fn := tool.Get("function")
		if !fn.Exists() {
			fn = tool
		}
		name := strings.TrimSpace(fn.Get("name").String())
		if name == "" {
			return true
		}
		schema := fn.Get("parameters")
		if !schema.Exists() {
			schema = tool.Get("inputSchema")
		}
		raw := json.RawMessage(`{"type":"object","properties":{}}`)
		if schema.Exists() && schema.IsObject() {
			raw = json.RawMessage(schema.Raw)
		}
		specs = append(specs, kiroToolWrapper{
			ToolSpecification: kiroToolSpec{
				Name:        name,
				Description: fn.Get("description").String(),
				InputSchema: kiroInputSchema{JSON: raw},
			},
		})
		return true
	})
	if len(specs) == 0 {
		return nil
	}
	return specs
}

// kiroParseArguments normalizes an OpenAI tool-call argument string into a JSON object.
// A malformed string is preserved under "_raw" so the model can see and correct its own
// output instead of the call silently vanishing.
func kiroParseArguments(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	if gjson.Valid(trimmed) && gjson.Parse(trimmed).IsObject() {
		return json.RawMessage(trimmed)
	}
	wrapped, err := json.Marshal(map[string]string{"_raw": raw})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return wrapped
}

// kiroToolUsesFrom extracts assistant tool calls from an OpenAI message.
func kiroToolUsesFrom(message gjson.Result) []kiroToolUse {
	calls := message.Get("tool_calls")
	if !calls.IsArray() {
		return nil
	}
	uses := make([]kiroToolUse, 0, len(calls.Array()))
	index := 0
	calls.ForEach(func(_, call gjson.Result) bool {
		id := call.Get("id").String()
		if strings.TrimSpace(id) == "" {
			id = "tooluse_" + strconv.Itoa(index)
		}
		name := call.Get("function.name").String()
		if strings.TrimSpace(name) == "" {
			name = call.Get("name").String()
		}
		args := call.Get("function.arguments").String()
		if args == "" {
			args = call.Get("arguments").String()
		}
		uses = append(uses, kiroToolUse{
			ToolUseID: id,
			Name:      name,
			Input:     kiroParseArguments(args),
		})
		index++
		return true
	})
	if len(uses) == 0 {
		return nil
	}
	return uses
}

// BuildKiroRequest converts an OpenAI chat completions payload into a Kiro
// generateAssistantResponse body.
//
// Kiro's history must alternate user -> assistant. System prompts are prepended to the
// first user message because the API has no system role, and consecutive same-role
// messages are merged to keep the alternation valid. The last user turn becomes
// currentMessage; everything before it becomes history.
func BuildKiroRequest(input KiroRequestInput) ([]byte, error) {
	root := gjson.ParseBytes(input.Payload)
	messages := root.Get("messages")

	var systemParts []string
	turns := make([]*kiroTurn, 0, 8)

	messages.ForEach(func(_, message gjson.Result) bool {
		role := message.Get("role").String()
		switch role {
		case "system", "developer":
			if text := KiroContentToText(message.Get("content")); text != "" {
				systemParts = append(systemParts, text)
			}
			return true
		case "tool", "function":
			text := KiroContentToText(message.Get("content"))
			if text == "" {
				text = kiroEmptyToolOutput
			}
			result := kiroToolResult{
				ToolUseID: message.Get("tool_call_id").String(),
				Content:   []kiroToolResultContent{{Text: text}},
				Status:    "success",
			}
			if last := lastKiroTurn(turns); last != nil && last.role == "user" {
				last.toolResults = append(last.toolResults, result)
			} else {
				turns = append(turns, &kiroTurn{role: "user", toolResults: []kiroToolResult{result}})
			}
			return true
		}

		if role != "user" && role != "assistant" {
			return true
		}

		text := KiroContentToText(message.Get("content"))
		var toolUses []kiroToolUse
		if role == "assistant" {
			toolUses = kiroToolUsesFrom(message)
		}

		if last := lastKiroTurn(turns); last != nil && last.role == role {
			last.text = joinKiroText(last.text, text)
			last.toolUses = append(last.toolUses, toolUses...)
			return true
		}
		turns = append(turns, &kiroTurn{role: role, text: text, toolUses: toolUses})
		return true
	})

	if systemText := strings.Join(systemParts, "\n\n"); systemText != "" {
		var firstUser *kiroTurn
		for _, turn := range turns {
			if turn.role == "user" {
				firstUser = turn
				break
			}
		}
		if firstUser != nil {
			firstUser.text = strings.TrimSpace(systemText + "\n\n" + firstUser.text)
		} else {
			turns = append([]*kiroTurn{{role: "user", text: systemText}}, turns...)
		}
	}

	lastUserIndex := -1
	for i := len(turns) - 1; i >= 0; i-- {
		if turns[i].role == "user" {
			lastUserIndex = i
			break
		}
	}
	if lastUserIndex == -1 {
		turns = append(turns, &kiroTurn{role: "user", text: "Continue."})
		lastUserIndex = len(turns) - 1
	}

	current := turns[lastUserIndex]
	history := make([]kiroHistoryEntry, 0, lastUserIndex)
	for _, turn := range turns[:lastUserIndex] {
		if turn.role == "user" {
			entry := &kiroUserInputMessage{
				Content: kiroTextOrPlaceholder(turn.text),
				Origin:  KiroOrigin,
				ModelID: input.Model,
			}
			if len(turn.toolResults) > 0 {
				entry.UserInputMessageContext = &kiroUserInputMessageContext{ToolResults: turn.toolResults}
			}
			history = append(history, kiroHistoryEntry{UserInputMessage: entry})
			continue
		}
		history = append(history, kiroHistoryEntry{AssistantResponseMessage: &kiroAssistantResponseMessage{
			Content:  turn.text,
			ToolUses: turn.toolUses,
		}})
	}

	currentContext := &kiroUserInputMessageContext{
		Tools:       kiroToolSpecifications(root.Get("tools")),
		ToolResults: current.toolResults,
	}

	conversationID := strings.TrimSpace(input.ConversationID)
	if conversationID == "" {
		conversationID = uuid.NewString()
	}

	body := kiroRequestBody{
		ProfileArn: input.ProfileArn,
		ConversationState: kiroConversationState{
			ChatTriggerType: "MANUAL",
			AgentTaskType:   "vibe",
			ConversationID:  conversationID,
			History:         history,
			CurrentMessage: kiroCurrentMessage{
				UserInputMessage: kiroUserInputMessage{
					Content:                 kiroCurrentTextOrPlaceholder(current.text),
					UserInputMessageContext: currentContext,
					Origin:                  KiroOrigin,
					ModelID:                 input.Model,
				},
			},
		},
	}
	return json.Marshal(body)
}

func lastKiroTurn(turns []*kiroTurn) *kiroTurn {
	if len(turns) == 0 {
		return nil
	}
	return turns[len(turns)-1]
}

func joinKiroText(existing, next string) string {
	if existing == "" {
		return next
	}
	if next == "" {
		return existing
	}
	return existing + "\n" + next
}

func kiroTextOrPlaceholder(text string) string {
	if strings.TrimSpace(text) == "" {
		return kiroPlaceholderText
	}
	return text
}

func kiroCurrentTextOrPlaceholder(text string) string {
	if strings.TrimSpace(text) == "" {
		return "Continue."
	}
	return text
}

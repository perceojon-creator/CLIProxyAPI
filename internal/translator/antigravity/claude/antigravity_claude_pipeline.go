package claude

import (
	"strings"

	sigcompat "github.com/router-for-me/CLIProxyAPI/v7/internal/signature"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	translatorcommon "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/common"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/translator/gemini/common"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// extractAntigravityClaudeSystemParts extracts and normalizes system instructions from the Claude request,
// filtering out attribution headers and producing Google Antigravity system parts.
func extractAntigravityClaudeSystemParts(rawJSON []byte) [][]byte {
	systemParts := make([][]byte, 0, 2)
	systemResult := gjson.GetBytes(rawJSON, "system")
	if systemResult.IsArray() {
		systemResults := systemResult.Array()
		for i := 0; i < len(systemResults); i++ {
			systemPromptResult := systemResults[i]
			systemTypePromptResult := systemPromptResult.Get("type")
			if systemTypePromptResult.Type == gjson.String && systemTypePromptResult.String() == "text" {
				systemPrompt := systemPromptResult.Get("text").String()
				if util.IsClaudeCodeAttributionSystemText(systemPrompt) {
					continue
				}
				partJSON := []byte(`{}`)
				if systemPrompt != "" {
					partJSON, _ = sjson.SetBytes(partJSON, "text", systemPrompt)
				}
				systemParts = append(systemParts, partJSON)
			}
		}
	} else if systemResult.Type == gjson.String && !util.IsClaudeCodeAttributionSystemText(systemResult.String()) {
		partJSON := []byte(`{"text":""}`)
		partJSON, _ = sjson.SetBytes(partJSON, "text", systemResult.String())
		systemParts = append(systemParts, partJSON)
	}
	return systemParts
}

// extractAntigravityClaudeTools extracts tool declarations from the Claude request, sanitizes JSON schemas,
// filters non-standard attributes, and formats them for Antigravity function calling.
func extractAntigravityClaudeTools(rawJSON []byte, functionNameMap map[string]string) ([]byte, int) {
	var toolsJSON []byte
	toolDeclCount := 0
	allowedToolKeys := []string{"name", "description", "behavior", "parameters", "parametersJsonSchema", "response", "responseJsonSchema"}
	toolsResult := gjson.GetBytes(rawJSON, "tools")
	if !toolsResult.IsArray() {
		return toolsJSON, toolDeclCount
	}

	var functionDeclarations [][]byte
	toolsResults := toolsResult.Array()
	for i := 0; i < len(toolsResults); i++ {
		toolResult := toolsResults[i]
		if isClaudeTypedWebSearchToolType(toolResult.Get("type").String()) {
			continue
		}
		inputSchemaResult := toolResult.Get("input_schema")
		if inputSchemaResult.Exists() && inputSchemaResult.IsObject() {
			inputSchema := util.CleanJSONSchemaForAntigravity(inputSchemaResult.Raw)
			tool, _ := sjson.DeleteBytes([]byte(toolResult.Raw), "input_schema")
			tool, _ = sjson.SetRawBytes(tool, "parametersJsonSchema", []byte(inputSchema))
			nameResult := gjson.GetBytes(tool, "name")
			originalName := nameResult.String()
			mappedName := util.MapSanitizedFunctionName(functionNameMap, originalName)
			if nameResult.Type != gjson.String || mappedName != originalName {
				tool, _ = sjson.SetBytes(tool, "name", mappedName)
			}
			for toolKey := range gjson.ParseBytes(tool).Map() {
				if util.InArray(allowedToolKeys, toolKey) {
					continue
				}
				tool, _ = sjson.DeleteBytes(tool, toolKey)
			}
			functionDeclarations = append(functionDeclarations, tool)
		}
	}
	if len(functionDeclarations) > 0 {
		deduplicated := util.DeduplicateFunctionDeclarations(translatorcommon.JoinRawArray(functionDeclarations))
		toolDeclCount = len(gjson.ParseBytes(deduplicated).Array())
		if toolDeclCount > 0 {
			functionToolNode := []byte(`{"functionDeclarations":[]}`)
			functionToolNode, _ = sjson.SetRawBytes(functionToolNode, "functionDeclarations", deduplicated)
			toolsJSON = translatorcommon.JoinRawArray([][]byte{functionToolNode})
		}
	}
	return toolsJSON, toolDeclCount
}

// claudeMessageCursor maintains the iteration state for parsing a single Claude message content stream.
type claudeMessageCursor struct {
	modelName                 string
	messageIndex              int
	originalRole              string
	role                      string
	contentResults            []gjson.Result
	partItems                 [][]byte
	pendingDetachedSignature  string
	pendingDetachedTargetKind string
	toolNameByID              map[string]string
	pendingToolUseIDs         *[]string
	functionNameMap           map[string]string
	enableThoughtTranslate    *bool
}

func (c *claudeMessageCursor) appendDetachedCarrier(signature string, _ bool) {
	carrier := []byte(`{"text":"","thoughtSignature":""}`)
	carrier, _ = sjson.SetBytes(carrier, "thoughtSignature", signature)
	c.partItems = append(c.partItems, carrier)
}

func (c *claudeMessageCursor) clearPendingDetachedSignature() {
	c.pendingDetachedSignature = ""
	c.pendingDetachedTargetKind = ""
}

func (c *claudeMessageCursor) setPendingDetachedSignature(signature, targetKind string) {
	if c.pendingDetachedSignature != "" {
		c.appendDetachedCarrier(c.pendingDetachedSignature, true)
	}
	c.pendingDetachedSignature = signature
	c.pendingDetachedTargetKind = targetKind
}

func inspectNextAcceptsDetachedSignature(contentResults []gjson.Result, j int) (bool, string) {
	if j+1 >= len(contentResults) {
		return false, geminiClaudeCarrierAny
	}
	switch contentResults[j+1].Get("type").String() {
	case "text":
		return true, geminiClaudeCarrierText
	case "tool_use":
		return true, geminiClaudeCarrierFunction
	default:
		return false, geminiClaudeCarrierAny
	}
}

func attachTrailingCarrier(c *claudeMessageCursor, signature string, markedCarrier bool, carrierTargetKind string, bindBackward bool) {
	attached := false
	foundSemanticPart := false
	for partIndex := len(c.partItems) - 1; partIndex >= 0; partIndex-- {
		part := gjson.ParseBytes(c.partItems[partIndex])
		partTargetKind := ""
		switch {
		case part.Get("functionCall").Exists():
			partTargetKind = geminiClaudeCarrierFunction
		case part.Get("text").Exists() && part.Get("text").String() != "":
			partTargetKind = geminiClaudeCarrierText
		default:
			continue
		}
		foundSemanticPart = true
		if markedCarrier && carrierTargetKind != geminiClaudeCarrierAny && carrierTargetKind != partTargetKind {
			break
		}
		partSignature := strings.TrimSpace(part.Get("thoughtSignature").String())
		replaceFallback := bindBackward && partTargetKind == geminiClaudeCarrierFunction && partSignature == sigcompat.GeminiSkipThoughtSignatureValidator
		if partSignature == "" || replaceFallback {
			c.partItems[partIndex], _ = sjson.SetBytes(c.partItems[partIndex], "thoughtSignature", signature)
			attached = true
		}
		break
	}
	if !attached && (foundSemanticPart || bindBackward) {
		c.appendDetachedCarrier(signature, false)
	} else if !attached {
		c.setPendingDetachedSignature(signature, carrierTargetKind)
	}
}

func handleNonEmptyThinking(
	c *claudeMessageCursor,
	thinkingText string,
	signature string,
	signatureFromPendingCarrier bool,
	markedCarrier bool,
	validCarrier bool,
	carrierDirection string,
	carrierTargetKind string,
	nextAcceptsDetachedSignature bool,
	nextTargetKind string,
	isGeminiSignature bool,
) {
	partJSON := []byte(`{}`)
	partJSON, _ = sjson.SetBytes(partJSON, "thought", true)
	partJSON, _ = sjson.SetBytes(partJSON, "text", thinkingText)
	if signatureFromPendingCarrier {
		partJSON, _ = sjson.SetBytes(partJSON, "thoughtSignature", signature)
	} else if markedCarrier {
		carrierTargetsNext := carrierTargetKind == geminiClaudeCarrierAny || carrierTargetKind == nextTargetKind
		if validCarrier && carrierDirection == geminiClaudeCarrierStandalone && (carrierTargetKind == geminiClaudeCarrierText || carrierTargetKind == geminiClaudeCarrierAny) {
			partJSON, _ = sjson.SetBytes(partJSON, "thoughtSignature", signature)
		} else if validCarrier && carrierDirection == geminiClaudeCarrierNext && nextAcceptsDetachedSignature && carrierTargetsNext {
			c.setPendingDetachedSignature(signature, carrierTargetKind)
		}
	} else if isGeminiSignature && nextAcceptsDetachedSignature {
		c.setPendingDetachedSignature(signature, nextTargetKind)
	} else if signature != "" {
		partJSON, _ = sjson.SetBytes(partJSON, "thoughtSignature", signature)
	}
	c.partItems = append(c.partItems, partJSON)
}

func handleEmptyThinkingCarrier(
	c *claudeMessageCursor,
	j int,
	signature string,
	markedCarrier bool,
	validCarrier bool,
	carrierDirection string,
	carrierTargetKind string,
	nextAcceptsDetachedSignature bool,
	nextTargetKind string,
	isGeminiSignature bool,
) {
	if !isGeminiSignature {
		logDroppedAntigravityEmptyThinking(c.modelName, c.messageIndex, j)
		return
	}
	if markedCarrier && !validCarrier {
		return
	}
	if markedCarrier && carrierDirection == geminiClaudeCarrierNext {
		if geminiClaudeCarrierMatchesAdjacent(c.contentResults, j, carrierDirection, carrierTargetKind) {
			c.setPendingDetachedSignature(signature, carrierTargetKind)
		}
		return
	}
	if markedCarrier && carrierDirection == geminiClaudeCarrierStandalone {
		c.appendDetachedCarrier(signature, false)
		return
	}

	bindBackward := markedCarrier && carrierDirection == geminiClaudeCarrierPrevious
	if bindBackward && !geminiClaudeCarrierMatchesAdjacent(c.contentResults, j, carrierDirection, carrierTargetKind) {
		return
	}
	if !bindBackward && nextAcceptsDetachedSignature {
		c.setPendingDetachedSignature(signature, nextTargetKind)
		return
	}

	attachTrailingCarrier(c, signature, markedCarrier, carrierTargetKind, bindBackward)
}

func handleThinkingContent(c *claudeMessageCursor, j int, contentResult gjson.Result) {
	if c.originalRole != "assistant" {
		return
	}
	thinkingText := thinking.GetThinkingText(contentResult)
	signatureResult := contentResult.Get("signature")
	signature := resolveThinkingSignature(c.modelName, thinkingText, signatureResult.String())
	if signature != "" && c.pendingDetachedSignature != "" {
		if c.pendingDetachedSignature != signature {
			c.appendDetachedCarrier(c.pendingDetachedSignature, false)
		}
		c.clearPendingDetachedSignature()
	}
	signatureFromPendingCarrier := false
	if signature == "" && thinkingText != "" && c.pendingDetachedSignature != "" {
		if c.pendingDetachedTargetKind == "" || c.pendingDetachedTargetKind == geminiClaudeCarrierAny || c.pendingDetachedTargetKind == geminiClaudeCarrierText {
			signature = c.pendingDetachedSignature
			signatureFromPendingCarrier = true
		} else {
			c.appendDetachedCarrier(c.pendingDetachedSignature, true)
		}
		c.clearPendingDetachedSignature()
	}

	isGeminiSignature := sigcompat.SignatureProviderFromModelName(c.modelName) == sigcompat.SignatureProviderGemini
	isUnsigned := !hasResolvedThinkingSignature(c.modelName, signature)

	if isUnsigned && !isGeminiSignature {
		logDroppedAntigravityThinkingSignature(c.modelName, c.messageIndex, j, thinkingText, signatureResult)
		*c.enableThoughtTranslate = false
		return
	}

	nextAcceptsDetachedSignature, nextTargetKind := inspectNextAcceptsDetachedSignature(c.contentResults, j)
	_, carrierDirection, carrierTargetKind, markedCarrier, validCarrier := decodeGeminiClaudeCarrierSignature(signatureResult.String())

	if thinkingText != "" {
		handleNonEmptyThinking(c, thinkingText, signature, signatureFromPendingCarrier, markedCarrier, validCarrier, carrierDirection, carrierTargetKind, nextAcceptsDetachedSignature, nextTargetKind, isGeminiSignature)
		return
	}

	handleEmptyThinkingCarrier(c, j, signature, markedCarrier, validCarrier, carrierDirection, carrierTargetKind, nextAcceptsDetachedSignature, nextTargetKind, isGeminiSignature)
}

func handleTextContent(c *claudeMessageCursor, contentResult gjson.Result) {
	prompt := contentResult.Get("text").String()
	if prompt == "" {
		return
	}
	partJSON := []byte(`{}`)
	partJSON, _ = sjson.SetBytes(partJSON, "text", prompt)
	if c.pendingDetachedSignature != "" {
		if c.pendingDetachedTargetKind == "" || c.pendingDetachedTargetKind == geminiClaudeCarrierAny || c.pendingDetachedTargetKind == geminiClaudeCarrierText {
			partJSON, _ = sjson.SetBytes(partJSON, "thoughtSignature", c.pendingDetachedSignature)
		} else {
			c.appendDetachedCarrier(c.pendingDetachedSignature, true)
		}
		c.clearPendingDetachedSignature()
	}
	c.partItems = append(c.partItems, partJSON)
}

func parseToolUseArgs(argsResult gjson.Result) string {
	if argsResult.IsObject() {
		return argsResult.Raw
	}
	if !argsResult.Exists() {
		return ""
	}
	switch argsResult.Type {
	case gjson.String:
		parsed := gjson.Parse(argsResult.String())
		if parsed.IsObject() {
			return parsed.Raw
		}
		return argsResult.Raw
	case gjson.Null:
		return `{}`
	default:
		return argsResult.Raw
	}
}

func resolveToolUseCarrierSignature(c *claudeMessageCursor, contentResult gjson.Result, j int) string {
	signature := resolveToolUseThoughtSignature(c.modelName, contentResult, true)
	if c.pendingDetachedSignature != "" {
		pendingMatchesTool := c.pendingDetachedTargetKind == "" || c.pendingDetachedTargetKind == geminiClaudeCarrierAny || c.pendingDetachedTargetKind == geminiClaudeCarrierFunction
		if pendingMatchesTool && (signature == "" || signature == sigcompat.GeminiSkipThoughtSignatureValidator) {
			signature = c.pendingDetachedSignature
		} else {
			c.appendDetachedCarrier(c.pendingDetachedSignature, true)
		}
		c.clearPendingDetachedSignature()
	}
	if signature == "" {
		logDroppedAntigravityToolUseSignature(c.modelName, c.messageIndex, j, contentResult)
	}
	return signature
}

func handleToolUseContent(c *claudeMessageCursor, j int, contentResult gjson.Result) {
	originalFunctionName := contentResult.Get("name").String()
	functionName := util.MapSanitizedFunctionName(c.functionNameMap, originalFunctionName)
	functionID := contentResult.Get("id").String()

	if functionID != "" && originalFunctionName != "" {
		c.toolNameByID[functionID] = originalFunctionName
	}

	argsRaw := parseToolUseArgs(contentResult.Get("input"))
	if argsRaw == "" {
		return
	}

	partJSON := []byte(`{}`)
	if signature := resolveToolUseCarrierSignature(c, contentResult, j); signature != "" {
		partJSON, _ = sjson.SetBytes(partJSON, "thoughtSignature", signature)
	}

	if functionID != "" {
		partJSON, _ = sjson.SetBytes(partJSON, "functionCall.id", functionID)
	}
	partJSON, _ = sjson.SetBytes(partJSON, "functionCall.name", functionName)
	partJSON, _ = sjson.SetRawBytes(partJSON, "functionCall.args", []byte(argsRaw))
	c.partItems = append(c.partItems, partJSON)
	if c.originalRole == "assistant" {
		*c.pendingToolUseIDs = append(*c.pendingToolUseIDs, functionID)
	}
}

func handleToolResultContent(c *claudeMessageCursor, contentResult gjson.Result) {
	toolCallID := contentResult.Get("tool_use_id").String()
	if toolCallID == "" {
		return
	}
	funcName, ok := c.toolNameByID[toolCallID]
	if !ok {
		parts := strings.Split(toolCallID, "-")
		if len(parts) > 2 {
			funcName = strings.Join(parts[:len(parts)-2], "-")
		}
		if funcName == "" {
			funcName = toolCallID
		}
		log.Warnf("antigravity claude request: tool_result references unknown tool_use_id=%s, derived function name=%s", toolCallID, funcName)
	}
	functionResponseResult := contentResult.Get("content")

	functionResponseJSON := []byte(`{}`)
	functionResponseJSON, _ = sjson.SetBytes(functionResponseJSON, "id", toolCallID)
	functionResponseJSON, _ = sjson.SetBytes(functionResponseJSON, "name", util.MapSanitizedFunctionName(c.functionNameMap, funcName))

	functionResponseJSON = formatToolResultResponse(functionResponseJSON, functionResponseResult)

	partJSON := []byte(`{}`)
	partJSON, _ = sjson.SetRawBytes(partJSON, "functionResponse", functionResponseJSON)
	c.partItems = append(c.partItems, partJSON)
}

func formatToolResultArrayResponse(targetJSON []byte, frResults []gjson.Result) []byte {
	nonImageItems := make([][]byte, 0, len(frResults))
	imagePartItems := make([][]byte, 0, 2)
	for _, fr := range frResults {
		if fr.Get("type").String() == "image" && fr.Get("source.type").String() == "base64" {
			inlineDataJSON := []byte(`{}`)
			if mimeType := fr.Get("source.media_type").String(); mimeType != "" {
				inlineDataJSON, _ = sjson.SetBytes(inlineDataJSON, "mimeType", mimeType)
			}
			if data := fr.Get("source.data").String(); data != "" {
				inlineDataJSON, _ = sjson.SetBytes(inlineDataJSON, "data", data)
			}

			imagePartJSON := []byte(`{}`)
			imagePartJSON, _ = sjson.SetRawBytes(imagePartJSON, "inlineData", inlineDataJSON)
			imagePartItems = append(imagePartItems, imagePartJSON)
			continue
		}
		nonImageItems = append(nonImageItems, []byte(fr.Raw))
	}

	if len(nonImageItems) == 1 {
		targetJSON, _ = sjson.SetRawBytes(targetJSON, "response.result", nonImageItems[0])
	} else if len(nonImageItems) > 1 {
		targetJSON, _ = sjson.SetRawBytes(targetJSON, "response.result", translatorcommon.JoinRawArray(nonImageItems))
	} else {
		targetJSON, _ = sjson.SetBytes(targetJSON, "response.result", "")
	}

	if len(imagePartItems) > 0 {
		targetJSON, _ = sjson.SetRawBytes(targetJSON, "parts", translatorcommon.JoinRawArray(imagePartItems))
	}
	return targetJSON
}

func formatToolResultResponse(targetJSON []byte, frResult gjson.Result) []byte {
	if frResult.Type == gjson.String {
		targetJSON, _ = sjson.SetBytes(targetJSON, "response.result", frResult.String())
		return targetJSON
	}
	if frResult.IsArray() {
		return formatToolResultArrayResponse(targetJSON, frResult.Array())
	}
	if frResult.IsObject() {
		if frResult.Get("type").String() == "image" && frResult.Get("source.type").String() == "base64" {
			inlineDataJSON := []byte(`{}`)
			if mimeType := frResult.Get("source.media_type").String(); mimeType != "" {
				inlineDataJSON, _ = sjson.SetBytes(inlineDataJSON, "mimeType", mimeType)
			}
			if data := frResult.Get("source.data").String(); data != "" {
				inlineDataJSON, _ = sjson.SetBytes(inlineDataJSON, "data", data)
			}

			imagePartJSON := []byte(`{}`)
			imagePartJSON, _ = sjson.SetRawBytes(imagePartJSON, "inlineData", inlineDataJSON)
			targetJSON, _ = sjson.SetRawBytes(targetJSON, "parts", translatorcommon.JoinRawArray([][]byte{imagePartJSON}))
			targetJSON, _ = sjson.SetBytes(targetJSON, "response.result", "")
			return targetJSON
		}
		targetJSON, _ = sjson.SetRawBytes(targetJSON, "response.result", []byte(frResult.Raw))
		return targetJSON
	}
	if frResult.Raw != "" {
		targetJSON, _ = sjson.SetRawBytes(targetJSON, "response.result", []byte(frResult.Raw))
		return targetJSON
	}
	targetJSON, _ = sjson.SetBytes(targetJSON, "response.result", "")
	return targetJSON
}

func handleImageContent(c *claudeMessageCursor, contentResult gjson.Result) {
	sourceResult := contentResult.Get("source")
	if sourceResult.Get("type").String() == "base64" {
		inlineDataJSON := []byte(`{}`)
		if mimeType := sourceResult.Get("media_type").String(); mimeType != "" {
			inlineDataJSON, _ = sjson.SetBytes(inlineDataJSON, "mimeType", mimeType)
		}
		if data := sourceResult.Get("data").String(); data != "" {
			inlineDataJSON, _ = sjson.SetBytes(inlineDataJSON, "data", data)
		}

		partJSON := []byte(`{}`)
		partJSON, _ = sjson.SetRawBytes(partJSON, "inlineData", inlineDataJSON)
		c.partItems = append(c.partItems, partJSON)
	}
}

func reorderModelParts(partItems [][]byte) [][]byte {
	if len(partItems) <= 1 {
		return partItems
	}
	var thinkingParts [][]byte
	var regularParts [][]byte
	var trailingParts [][]byte
	needsReorder := false
	previousCategory := -1
	seenFunctionCall := false
	for _, partJSON := range partItems {
		part := gjson.ParseBytes(partJSON)
		category := 1
		isSignatureCarrier := part.Get("text").Exists() && part.Get("text").String() == "" && strings.TrimSpace(part.Get("thoughtSignature").String()) != ""
		isFunctionTailCarrier := isSignatureCarrier && seenFunctionCall
		if part.Get("thought").Bool() {
			category = 0
			thinkingParts = append(thinkingParts, partJSON)
		} else if part.Get("functionCall").Exists() || isFunctionTailCarrier {
			category = 2
			trailingParts = append(trailingParts, partJSON)
			seenFunctionCall = seenFunctionCall || part.Get("functionCall").Exists()
		} else {
			regularParts = append(regularParts, partJSON)
		}
		needsReorder = needsReorder || category < previousCategory
		previousCategory = category
	}
	if !needsReorder {
		return partItems
	}
	newParts := make([][]byte, 0, len(partItems))
	newParts = append(newParts, thinkingParts...)
	newParts = append(newParts, regularParts...)
	newParts = append(newParts, trailingParts...)
	return newParts
}

func processClaudeMessageParts(cursor *claudeMessageCursor) [][]byte {
	for j, contentResult := range cursor.contentResults {
		switch contentResult.Get("type").String() {
		case "thinking":
			handleThinkingContent(cursor, j, contentResult)
		case "text":
			handleTextContent(cursor, contentResult)
		case "tool_use":
			handleToolUseContent(cursor, j, contentResult)
		case "tool_result":
			handleToolResultContent(cursor, contentResult)
		case "image":
			handleImageContent(cursor, contentResult)
		}
	}

	if cursor.pendingDetachedSignature != "" {
		cursor.appendDetachedCarrier(cursor.pendingDetachedSignature, false)
		cursor.clearPendingDetachedSignature()
	}

	if len(cursor.partItems) == 0 {
		return nil
	}

	if cursor.role == "model" {
		cursor.partItems = reorderModelParts(cursor.partItems)
	}
	return cursor.partItems
}

// extractAntigravityClaudeContents transforms conversation messages, thinking blocks, image modalities,
// and tool call / tool result structures into Antigravity model/user contents with strict signature carrier preservation.
func extractAntigravityClaudeContents(modelName string, rawJSON []byte, functionNameMap map[string]string) ([][]byte, bool) {
	enableThoughtTranslate := true
	contentItems := translatorcommon.NewRawArrayItems(gjson.GetBytes(rawJSON, "messages.#").Int())

	toolNameByID := make(map[string]string)
	var pendingToolUseIDs []string

	messagesResult := gjson.GetBytes(rawJSON, "messages")
	if !messagesResult.IsArray() {
		return contentItems, enableThoughtTranslate
	}

	messageResults := messagesResult.Array()
	for i, messageResult := range messageResults {
		roleResult := messageResult.Get("role")
		if roleResult.Type != gjson.String {
			continue
		}
		originalRole := roleResult.String()
		var precedingToolUseIDs []string
		if originalRole != "system" && originalRole != "developer" {
			precedingToolUseIDs = pendingToolUseIDs
			pendingToolUseIDs = nil
		}
		role := originalRole
		if role == "assistant" {
			role = "model"
		} else if role == "system" || role == "developer" {
			role = "user"
		}

		contentsResult := messageResult.Get("content")
		if originalRole == "system" || originalRole == "developer" {
			if reminderText, ok := translatorcommon.ClaudeMessageSystemReminderText(contentsResult); ok {
				partJSON := []byte(`{}`)
				partJSON, _ = sjson.SetBytes(partJSON, "text", reminderText)
				contentItems = append(contentItems, antigravityClaudeContent(role, [][]byte{partJSON}))
			}
			continue
		}

		if contentsResult.IsArray() {
			if originalRole == "user" {
				contentsResult = translatorcommon.AlignClaudeToolResults(contentsResult, precedingToolUseIDs)
			}
			cursor := &claudeMessageCursor{
				modelName:              modelName,
				messageIndex:           i,
				originalRole:           originalRole,
				role:                   role,
				contentResults:         contentsResult.Array(),
				partItems:              make([][]byte, 0, 4),
				toolNameByID:           toolNameByID,
				pendingToolUseIDs:      &pendingToolUseIDs,
				functionNameMap:        functionNameMap,
				enableThoughtTranslate: &enableThoughtTranslate,
			}

			parts := processClaudeMessageParts(cursor)
			if len(parts) > 0 {
				contentItems = append(contentItems, antigravityClaudeContent(role, parts))
			}
		} else if contentsResult.Type == gjson.String {
			partJSON := []byte(`{}`)
			if prompt := contentsResult.String(); prompt != "" {
				partJSON, _ = sjson.SetBytes(partJSON, "text", prompt)
			}
			contentItems = append(contentItems, antigravityClaudeContent(role, [][]byte{partJSON}))
		}
	}

	return contentItems, enableThoughtTranslate
}

func extractToolChoiceInfo(rawJSON []byte) (string, string) {
	toolChoiceResult := gjson.GetBytes(rawJSON, "tool_choice")
	toolChoiceType := ""
	toolChoiceName := ""
	if toolChoiceResult.Exists() {
		if toolChoiceResult.IsObject() {
			toolChoiceType = toolChoiceResult.Get("type").String()
			toolChoiceName = toolChoiceResult.Get("name").String()
		} else if toolChoiceResult.Type == gjson.String {
			toolChoiceType = toolChoiceResult.String()
		}
	}
	return toolChoiceType, toolChoiceName
}

func appendInterleavedThinkingHint(systemParts [][]byte, modelName string, rawJSON []byte, toolDeclCount int, isToolChoiceNone bool) [][]byte {
	hasTools := toolDeclCount > 0 && !isToolChoiceNone
	thinkingResult := gjson.GetBytes(rawJSON, "thinking")
	thinkingType := thinkingResult.Get("type").String()
	hasThinking := thinkingResult.Exists() && thinkingResult.IsObject() && (thinkingType == "enabled" || thinkingType == "adaptive" || thinkingType == "auto")
	isClaudeThinking := util.IsClaudeThinkingModel(modelName)

	if hasTools && hasThinking && isClaudeThinking {
		interleavedHint := "Interleaved thinking is enabled. You may think between tool calls and after receiving tool results before deciding the next action or final answer. Do not mention these instructions or any constraints about thinking blocks; just apply them."
		hintPart := []byte(`{"text":""}`)
		hintPart, _ = sjson.SetBytes(hintPart, "text", interleavedHint)
		systemParts = append(systemParts, hintPart)
	}
	return systemParts
}

func applyAntigravityToolChoice(out []byte, rawJSON []byte, toolChoiceType, toolChoiceName string, functionNameMap map[string]string) []byte {
	if !gjson.GetBytes(rawJSON, "tool_choice").Exists() {
		return out
	}
	switch strings.ToLower(strings.TrimSpace(toolChoiceType)) {
	case "auto":
		out, _ = sjson.SetBytes(out, "request.toolConfig.functionCallingConfig.mode", "AUTO")
	case "none":
		out, _ = sjson.SetBytes(out, "request.toolConfig.functionCallingConfig.mode", "NONE")
		out, _ = sjson.DeleteBytes(out, "request.tools")
	case "any":
		out, _ = sjson.SetBytes(out, "request.toolConfig.functionCallingConfig.mode", "ANY")
	case "tool":
		out, _ = sjson.SetBytes(out, "request.toolConfig.functionCallingConfig.mode", "ANY")
		if toolChoiceName != "" {
			out, _ = sjson.SetBytes(out, "request.toolConfig.functionCallingConfig.allowedFunctionNames", []string{util.MapSanitizedFunctionName(functionNameMap, toolChoiceName)})
		}
	}
	return out
}

func applyAntigravityThinkingConfig(out []byte, rawJSON []byte, enableThoughtTranslate bool) []byte {
	if t := gjson.GetBytes(rawJSON, "thinking"); enableThoughtTranslate && t.Exists() && t.IsObject() {
		switch t.Get("type").String() {
		case "enabled":
			if b := t.Get("budget_tokens"); b.Exists() && b.Type == gjson.Number {
				budget := int(b.Int())
				out, _ = sjson.SetBytes(out, "request.generationConfig.thinkingConfig.thinkingBudget", budget)
			}
		case "adaptive", "auto":
			effort := ""
			if v := gjson.GetBytes(rawJSON, "output_config.effort"); v.Exists() && v.Type == gjson.String {
				effort = strings.ToLower(strings.TrimSpace(v.String()))
			}
			if effort != "" {
				out, _ = sjson.SetBytes(out, "request.generationConfig.thinkingConfig.thinkingLevel", effort)
			} else {
				out, _ = sjson.SetBytes(out, "request.generationConfig.thinkingConfig.thinkingLevel", "high")
			}
		}
	}
	return out
}

func applyAntigravitySamplingConfig(out []byte, rawJSON []byte) []byte {
	if v := gjson.GetBytes(rawJSON, "temperature"); v.Exists() && v.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "request.generationConfig.temperature", v.Num)
	}
	if v := gjson.GetBytes(rawJSON, "top_p"); v.Exists() && v.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "request.generationConfig.topP", v.Num)
	}
	if v := gjson.GetBytes(rawJSON, "top_k"); v.Exists() && v.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "request.generationConfig.topK", v.Num)
	}
	if v := gjson.GetBytes(rawJSON, "max_tokens"); v.Exists() && v.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "request.generationConfig.maxOutputTokens", v.Num)
	}
	return out
}

func applyAntigravityGenerationConfig(out []byte, rawJSON []byte, enableThoughtTranslate bool) []byte {
	out = applyAntigravityThinkingConfig(out, rawJSON, enableThoughtTranslate)
	out = applyAntigravitySamplingConfig(out, rawJSON)
	return out
}

// assembleAntigravityClaudePayload constructs the final Antigravity request structure, mapping tool choice,
// interleaved thinking hints, safety settings, generation configs, and thought signature sanitization.
func assembleAntigravityClaudePayload(
	modelName string,
	rawJSON []byte,
	systemParts [][]byte,
	contentItems [][]byte,
	toolsJSON []byte,
	toolDeclCount int,
	functionNameMap map[string]string,
	enableThoughtTranslate bool,
) []byte {
	out := []byte(`{"model":"","request":{"contents":[]}}`)
	out, _ = sjson.SetBytes(out, "model", modelName)

	toolChoiceType, toolChoiceName := extractToolChoiceInfo(rawJSON)
	isToolChoiceNone := strings.EqualFold(strings.TrimSpace(toolChoiceType), "none")

	systemParts = appendInterleavedThinkingHint(systemParts, modelName, rawJSON, toolDeclCount, isToolChoiceNone)

	if len(systemParts) > 0 {
		out, _ = sjson.SetRawBytes(out, "request.systemInstruction", antigravityClaudeContent("user", systemParts))
	}
	if len(contentItems) > 0 {
		out = translatorcommon.SetRawArrayItems(out, "request.contents", translatorcommon.MergeAdjacentGeminiContents(contentItems))
	}
	if toolDeclCount > 0 && !isToolChoiceNone {
		out, _ = sjson.SetRawBytes(out, "request.tools", toolsJSON)
	}

	out = applyAntigravityToolChoice(out, rawJSON, toolChoiceType, toolChoiceName, functionNameMap)
	out = applyAntigravityGenerationConfig(out, rawJSON, enableThoughtTranslate)

	out = common.AttachDefaultSafetySettings(out, "request.safetySettings")
	if sigcompat.SignatureProviderFromModelName(modelName) == sigcompat.SignatureProviderGemini {
		out = sigcompat.SanitizeGeminiRequestThoughtSignatures(out, "request.contents")
	}

	return out
}

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
	if toolsResult.IsArray() {
		var functionDeclarations [][]byte
		toolsResults := toolsResult.Array()
		for i := 0; i < len(toolsResults); i++ {
			toolResult := toolsResults[i]
			if isClaudeTypedWebSearchToolType(toolResult.Get("type").String()) {
				continue
			}
			inputSchemaResult := toolResult.Get("input_schema")
			if inputSchemaResult.Exists() && inputSchemaResult.IsObject() {
				// Sanitize the input schema for Antigravity API compatibility
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
	}
	return toolsJSON, toolDeclCount
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
	numMessages := len(messageResults)
	for i := 0; i < numMessages; i++ {
		messageResult := messageResults[i]
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
		partItems := make([][]byte, 0, 4)
		appendDetachedCarrier := func(signature string, _ bool) {
			carrier := []byte(`{"text":"","thoughtSignature":""}`)
			carrier, _ = sjson.SetBytes(carrier, "thoughtSignature", signature)
			partItems = append(partItems, carrier)
		}
		pendingDetachedSignature := ""
		pendingDetachedTargetKind := ""
		clearPendingDetachedSignature := func() {
			pendingDetachedSignature = ""
			pendingDetachedTargetKind = ""
		}
		setPendingDetachedSignature := func(signature, targetKind string) {
			if pendingDetachedSignature != "" {
				appendDetachedCarrier(pendingDetachedSignature, true)
			}
			pendingDetachedSignature = signature
			pendingDetachedTargetKind = targetKind
		}
		contentsResult := messageResult.Get("content")
		if originalRole == "system" || originalRole == "developer" {
			if reminderText, ok := translatorcommon.ClaudeMessageSystemReminderText(contentsResult); ok {
				partJSON := []byte(`{}`)
				partJSON, _ = sjson.SetBytes(partJSON, "text", reminderText)
				partItems = append(partItems, partJSON)
				contentItems = append(contentItems, antigravityClaudeContent(role, partItems))
			}
			continue
		}
		if contentsResult.IsArray() {
			if originalRole == "user" {
				contentsResult = translatorcommon.AlignClaudeToolResults(contentsResult, precedingToolUseIDs)
			}
			contentResults := contentsResult.Array()
			numContents := len(contentResults)
			for j := 0; j < numContents; j++ {
				contentResult := contentResults[j]
				contentTypeResult := contentResult.Get("type")
				if contentTypeResult.Type == gjson.String && contentTypeResult.String() == "thinking" {
					if originalRole != "assistant" {
						continue
					}
					// Use GetThinkingText to handle wrapped thinking objects
					thinkingText := thinking.GetThinkingText(contentResult)
					signatureResult := contentResult.Get("signature")
					signature := resolveThinkingSignature(modelName, thinkingText, signatureResult.String())
					if signature != "" && pendingDetachedSignature != "" {
						if pendingDetachedSignature != signature {
							appendDetachedCarrier(pendingDetachedSignature, false)
						}
						clearPendingDetachedSignature()
					}
					signatureFromPendingCarrier := false
					if signature == "" && thinkingText != "" && pendingDetachedSignature != "" {
						if pendingDetachedTargetKind == "" || pendingDetachedTargetKind == geminiClaudeCarrierAny || pendingDetachedTargetKind == geminiClaudeCarrierText {
							signature = pendingDetachedSignature
							signatureFromPendingCarrier = true
						} else {
							appendDetachedCarrier(pendingDetachedSignature, true)
						}
						clearPendingDetachedSignature()
					}

					isGeminiSignature := sigcompat.SignatureProviderFromModelName(modelName) == sigcompat.SignatureProviderGemini

					// Skip unsigned thinking blocks instead of converting them to text for non-Gemini providers.
					isUnsigned := !hasResolvedThinkingSignature(modelName, signature)

					// If unsigned, skip entirely (don't convert to text)
					// Claude requires assistant messages to start with thinking blocks when thinking is enabled
					// Converting to text would break this requirement
					if isUnsigned && !isGeminiSignature {
						logDroppedAntigravityThinkingSignature(modelName, i, j, thinkingText, signatureResult)
						enableThoughtTranslate = false
						continue
					}

					nextAcceptsDetachedSignature := false
					nextTargetKind := geminiClaudeCarrierAny
					if j+1 < numContents {
						switch contentResults[j+1].Get("type").String() {
						case "text":
							nextAcceptsDetachedSignature = true
							nextTargetKind = geminiClaudeCarrierText
						case "tool_use":
							nextAcceptsDetachedSignature = true
							nextTargetKind = geminiClaudeCarrierFunction
						}
					}
					_, carrierDirection, carrierTargetKind, markedCarrier, validCarrier := decodeGeminiClaudeCarrierSignature(signatureResult.String())

					// Gemini places the signature on the visible text/function part that
					// follows hidden thought text. Keep the thought text, but defer its
					// opaque signature to that native neighboring part.
					if thinkingText != "" {
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
								setPendingDetachedSignature(signature, carrierTargetKind)
							}
						} else if isGeminiSignature && nextAcceptsDetachedSignature {
							setPendingDetachedSignature(signature, nextTargetKind)
						} else if signature != "" {
							partJSON, _ = sjson.SetBytes(partJSON, "thoughtSignature", signature)
						}
						partItems = append(partItems, partJSON)
						continue
					}

					if !isGeminiSignature {
						logDroppedAntigravityEmptyThinking(modelName, i, j)
						continue
					}
					if markedCarrier && !validCarrier {
						continue
					}
					if markedCarrier && carrierDirection == geminiClaudeCarrierNext {
						if geminiClaudeCarrierMatchesAdjacent(contentResults, j, carrierDirection, carrierTargetKind) {
							setPendingDetachedSignature(signature, carrierTargetKind)
						}
						continue
					}
					if markedCarrier && carrierDirection == geminiClaudeCarrierStandalone {
						appendDetachedCarrier(signature, false)
						continue
					}

					// Tagged trailing carriers bind backward even when another semantic
					// block follows. Untagged legacy carriers retain adjacency behavior.
					bindBackward := markedCarrier && carrierDirection == geminiClaudeCarrierPrevious
					if bindBackward && !geminiClaudeCarrierMatchesAdjacent(contentResults, j, carrierDirection, carrierTargetKind) {
						continue
					}
					if !bindBackward && nextAcceptsDetachedSignature {
						setPendingDetachedSignature(signature, nextTargetKind)
						continue
					}
					attached := false
					foundSemanticPart := false
					for partIndex := len(partItems) - 1; partIndex >= 0; partIndex-- {
						part := gjson.ParseBytes(partItems[partIndex])
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
							partItems[partIndex], _ = sjson.SetBytes(partItems[partIndex], "thoughtSignature", signature)
							attached = true
						}
						break
					}
					if !attached && (foundSemanticPart || bindBackward) {
						appendDetachedCarrier(signature, false)
					} else if !attached {
						setPendingDetachedSignature(signature, carrierTargetKind)
					}
				} else if contentTypeResult.Type == gjson.String && contentTypeResult.String() == "text" {
					prompt := contentResult.Get("text").String()
					// Skip empty text parts to avoid Gemini API error:
					// "required oneof field 'data' must have one initialized field"
					if prompt == "" {
						continue
					}
					partJSON := []byte(`{}`)
					partJSON, _ = sjson.SetBytes(partJSON, "text", prompt)
					if pendingDetachedSignature != "" {
						if pendingDetachedTargetKind == "" || pendingDetachedTargetKind == geminiClaudeCarrierAny || pendingDetachedTargetKind == geminiClaudeCarrierText {
							partJSON, _ = sjson.SetBytes(partJSON, "thoughtSignature", pendingDetachedSignature)
						} else {
							appendDetachedCarrier(pendingDetachedSignature, true)
						}
						clearPendingDetachedSignature()
					}
					partItems = append(partItems, partJSON)
				} else if contentTypeResult.Type == gjson.String && contentTypeResult.String() == "tool_use" {
					// NOTE: Do NOT inject dummy thinking blocks here.
					// Antigravity API validates signatures, so dummy values are rejected.

					originalFunctionName := contentResult.Get("name").String()
					functionName := util.MapSanitizedFunctionName(functionNameMap, originalFunctionName)
					argsResult := contentResult.Get("input")
					functionID := contentResult.Get("id").String()

					if functionID != "" && originalFunctionName != "" {
						toolNameByID[functionID] = originalFunctionName
					}

					// Preserve every present input as valid JSON for the function call.
					var argsRaw string
					if argsResult.IsObject() {
						argsRaw = argsResult.Raw
					} else if argsResult.Exists() {
						switch argsResult.Type {
						case gjson.String:
							// Parse JSON-encoded object strings while preserving other strings as JSON strings.
							parsed := gjson.Parse(argsResult.String())
							if parsed.IsObject() {
								argsRaw = parsed.Raw
							} else {
								argsRaw = argsResult.Raw
							}
						case gjson.Null:
							argsRaw = `{}`
						default:
							argsRaw = argsResult.Raw
						}
					}

					if argsRaw != "" {
						partJSON := []byte(`{}`)

						signature := resolveToolUseThoughtSignature(modelName, contentResult, true)
						if pendingDetachedSignature != "" {
							pendingMatchesTool := pendingDetachedTargetKind == "" || pendingDetachedTargetKind == geminiClaudeCarrierAny || pendingDetachedTargetKind == geminiClaudeCarrierFunction
							if pendingMatchesTool && (signature == "" || signature == sigcompat.GeminiSkipThoughtSignatureValidator) {
								signature = pendingDetachedSignature
							} else {
								appendDetachedCarrier(pendingDetachedSignature, true)
							}
							clearPendingDetachedSignature()
						}
						if signature != "" {
							partJSON, _ = sjson.SetBytes(partJSON, "thoughtSignature", signature)
						} else {
							logDroppedAntigravityToolUseSignature(modelName, i, j, contentResult)
						}

						if functionID != "" {
							partJSON, _ = sjson.SetBytes(partJSON, "functionCall.id", functionID)
						}
						partJSON, _ = sjson.SetBytes(partJSON, "functionCall.name", functionName)
						partJSON, _ = sjson.SetRawBytes(partJSON, "functionCall.args", []byte(argsRaw))
						partItems = append(partItems, partJSON)
						if originalRole == "assistant" {
							pendingToolUseIDs = append(pendingToolUseIDs, functionID)
						}
					}
				} else if contentTypeResult.Type == gjson.String && contentTypeResult.String() == "tool_result" {
					toolCallID := contentResult.Get("tool_use_id").String()
					if toolCallID != "" {
						funcName, ok := toolNameByID[toolCallID]
						if !ok {
							// Fallback: derive a semantic name from the ID by stripping
							// the last two dash-separated segments (e.g. "get_weather-call-123" → "get_weather").
							// Only use the raw ID as a last resort when the heuristic produces an empty string.
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
						functionResponseJSON, _ = sjson.SetBytes(functionResponseJSON, "name", util.MapSanitizedFunctionName(functionNameMap, funcName))

						responseData := ""
						if functionResponseResult.Type == gjson.String {
							responseData = functionResponseResult.String()
							functionResponseJSON, _ = sjson.SetBytes(functionResponseJSON, "response.result", responseData)
						} else if functionResponseResult.IsArray() {
							frResults := functionResponseResult.Array()
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
								functionResponseJSON, _ = sjson.SetRawBytes(functionResponseJSON, "response.result", nonImageItems[0])
							} else if len(nonImageItems) > 1 {
								functionResponseJSON, _ = sjson.SetRawBytes(functionResponseJSON, "response.result", translatorcommon.JoinRawArray(nonImageItems))
							} else {
								functionResponseJSON, _ = sjson.SetBytes(functionResponseJSON, "response.result", "")
							}

							// Place image data inside functionResponse.parts as inlineData
							// instead of as sibling parts in the outer content, to avoid
							// base64 data bloating the text context.
							if len(imagePartItems) > 0 {
								functionResponseJSON, _ = sjson.SetRawBytes(functionResponseJSON, "parts", translatorcommon.JoinRawArray(imagePartItems))
							}

						} else if functionResponseResult.IsObject() {
							if functionResponseResult.Get("type").String() == "image" && functionResponseResult.Get("source.type").String() == "base64" {
								inlineDataJSON := []byte(`{}`)
								if mimeType := functionResponseResult.Get("source.media_type").String(); mimeType != "" {
									inlineDataJSON, _ = sjson.SetBytes(inlineDataJSON, "mimeType", mimeType)
								}
								if data := functionResponseResult.Get("source.data").String(); data != "" {
									inlineDataJSON, _ = sjson.SetBytes(inlineDataJSON, "data", data)
								}

								imagePartJSON := []byte(`{}`)
								imagePartJSON, _ = sjson.SetRawBytes(imagePartJSON, "inlineData", inlineDataJSON)
								functionResponseJSON, _ = sjson.SetRawBytes(functionResponseJSON, "parts", translatorcommon.JoinRawArray([][]byte{imagePartJSON}))
								functionResponseJSON, _ = sjson.SetBytes(functionResponseJSON, "response.result", "")
							} else {
								functionResponseJSON, _ = sjson.SetRawBytes(functionResponseJSON, "response.result", []byte(functionResponseResult.Raw))
							}
						} else if functionResponseResult.Raw != "" {
							functionResponseJSON, _ = sjson.SetRawBytes(functionResponseJSON, "response.result", []byte(functionResponseResult.Raw))
						} else {
							// Content field is missing entirely — .Raw is empty which
							// causes sjson.SetRaw to produce invalid JSON (e.g. "result":}).
							functionResponseJSON, _ = sjson.SetBytes(functionResponseJSON, "response.result", "")
						}

						partJSON := []byte(`{}`)
						partJSON, _ = sjson.SetRawBytes(partJSON, "functionResponse", functionResponseJSON)
						partItems = append(partItems, partJSON)
					}
				} else if contentTypeResult.Type == gjson.String && contentTypeResult.String() == "image" {
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
						partItems = append(partItems, partJSON)
					}
				}
			}
			if pendingDetachedSignature != "" {
				appendDetachedCarrier(pendingDetachedSignature, false)
				clearPendingDetachedSignature()
			}

			// Reorder model parts: thinking first, regular content second, function calls and trailing signature carriers last.
			if len(partItems) == 0 {
				continue
			}
			clientContentJSON := antigravityClaudeContent(role, partItems)
			if role == "model" && len(partItems) > 1 {
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
				if needsReorder {
					newParts := make([][]byte, 0, len(partItems))
					newParts = append(newParts, thinkingParts...)
					newParts = append(newParts, regularParts...)
					newParts = append(newParts, trailingParts...)
					clientContentJSON, _ = sjson.SetRawBytes(clientContentJSON, "parts", translatorcommon.JoinRawArray(newParts))
				}
			}
			contentItems = append(contentItems, clientContentJSON)
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
	// Build output Antigravity request JSON
	out := []byte(`{"model":"","request":{"contents":[]}}`)
	out, _ = sjson.SetBytes(out, "model", modelName)

	// tool_choice metadata
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
	isToolChoiceNone := strings.EqualFold(strings.TrimSpace(toolChoiceType), "none")

	// Inject interleaved thinking hint when both tools and thinking are active
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

	if len(systemParts) > 0 {
		out, _ = sjson.SetRawBytes(out, "request.systemInstruction", antigravityClaudeContent("user", systemParts))
	}
	if len(contentItems) > 0 {
		out = translatorcommon.SetRawArrayItems(out, "request.contents", translatorcommon.MergeAdjacentGeminiContents(contentItems))
	}
	if toolDeclCount > 0 && !isToolChoiceNone {
		out, _ = sjson.SetRawBytes(out, "request.tools", toolsJSON)
	}

	// tool_choice
	if toolChoiceResult.Exists() {
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
	}

	// Map Anthropic thinking -> Gemini thinkingBudget/include_thoughts when type==enabled
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

	out = common.AttachDefaultSafetySettings(out, "request.safetySettings")
	if sigcompat.SignatureProviderFromModelName(modelName) == sigcompat.SignatureProviderGemini {
		out = sigcompat.SanitizeGeminiRequestThoughtSignatures(out, "request.contents")
	}

	return out
}

package chat_completions

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/translator/antigravity/gemini"
	translatorcommon "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/common"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/translator/gemini/common"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// stage1InitAntigravityOpenAIEnvelope initializes the base request envelope and applies
// model, generation configuration, thinking parameters, sampling options, structured outputs,
// and multimodal response configurations.
func stage1InitAntigravityOpenAIEnvelope(modelName string, rawJSON []byte) []byte {
	out := []byte(`{"project":"","request":{"contents":[]},"model":"gemini-2.5-pro"}`)
	out, _ = sjson.SetBytes(out, "model", modelName)

	if genConfig := gjson.GetBytes(rawJSON, "generationConfig"); genConfig.Exists() {
		out, _ = sjson.SetRawBytes(out, "request.generationConfig", []byte(genConfig.Raw))
	} else if genConfig := gjson.GetBytes(rawJSON, "generation_config"); genConfig.Exists() {
		out, _ = sjson.SetRawBytes(out, "request.generationConfig", []byte(genConfig.Raw))
	}

	out = applyAntigravityOpenAIThinkingConfig(out, rawJSON)
	out = applyAntigravityOpenAISamplingConfig(out, rawJSON)
	out = applyAntigravityOpenAIStructuredOutput(out, rawJSON)
	out = applyAntigravityOpenAIModalities(out, rawJSON)
	out = applyAntigravityOpenAIImageConfig(out, rawJSON)

	return out
}

func applyAntigravityOpenAIThinkingConfig(out, rawJSON []byte) []byte {
	re := gjson.GetBytes(rawJSON, "reasoning_effort")
	if re.Exists() {
		effort := strings.ToLower(strings.TrimSpace(re.String()))
		if effort != "" {
			thinkingPath := "request.generationConfig.thinkingConfig"
			if effort == "auto" {
				out, _ = sjson.SetBytes(out, thinkingPath+".thinkingBudget", -1)
			} else {
				out, _ = sjson.SetBytes(out, thinkingPath+".thinkingLevel", effort)
			}
		}
	}
	return applyOpenAIThinkingCompatibilityToAntigravity(out, rawJSON)
}

func applyAntigravityOpenAISamplingConfig(out, rawJSON []byte) []byte {
	if tr := gjson.GetBytes(rawJSON, "temperature"); tr.Exists() && tr.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "request.generationConfig.temperature", tr.Num)
	}
	if tpr := gjson.GetBytes(rawJSON, "top_p"); tpr.Exists() && tpr.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "request.generationConfig.topP", tpr.Num)
	}
	if tkr := gjson.GetBytes(rawJSON, "top_k"); tkr.Exists() && tkr.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "request.generationConfig.topK", tkr.Num)
	}
	if maxTok := gjson.GetBytes(rawJSON, "max_tokens"); maxTok.Exists() && maxTok.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "request.generationConfig.maxOutputTokens", maxTok.Num)
	} else if mct := gjson.GetBytes(rawJSON, "max_completion_tokens"); mct.Exists() && mct.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "request.generationConfig.maxOutputTokens", mct.Num)
	}
	if n := gjson.GetBytes(rawJSON, "n"); n.Exists() && n.Type == gjson.Number {
		if val := n.Int(); val > 1 {
			out, _ = sjson.SetBytes(out, "request.generationConfig.candidateCount", val)
		}
	}
	return out
}

func applyAntigravityOpenAIStructuredOutput(out, rawJSON []byte) []byte {
	responseFormat := gjson.GetBytes(rawJSON, "response_format")
	if !responseFormat.Exists() {
		return out
	}
	responseFormatType := strings.ToLower(strings.TrimSpace(responseFormat.Get("type").String()))
	switch responseFormatType {
	case "json_object", "json_schema":
		for _, schemaKey := range []string{"responseSchema", "responseJsonSchema", "response_schema", "response_json_schema"} {
			out, _ = sjson.DeleteBytes(out, "request.generationConfig."+schemaKey)
		}
		out, _ = sjson.SetBytes(out, "request.generationConfig.responseMimeType", "application/json")
		if responseFormatType == "json_schema" {
			if schema := responseFormat.Get("json_schema.schema"); schema.Exists() {
				out, _ = sjson.SetRawBytes(out, "request.generationConfig.responseSchema", []byte(schema.Raw))
			}
		}
	}
	return out
}

func applyAntigravityOpenAIModalities(out, rawJSON []byte) []byte {
	mods := gjson.GetBytes(rawJSON, "modalities")
	if !mods.Exists() || !mods.IsArray() {
		return out
	}
	var responseMods []string
	for _, m := range mods.Array() {
		switch strings.ToLower(m.String()) {
		case "text":
			responseMods = append(responseMods, "TEXT")
		case "image":
			responseMods = append(responseMods, "IMAGE")
		}
	}
	if len(responseMods) > 0 {
		out, _ = sjson.SetBytes(out, "request.generationConfig.responseModalities", responseMods)
	}
	return out
}

func applyAntigravityOpenAIImageConfig(out, rawJSON []byte) []byte {
	imgCfg := gjson.GetBytes(rawJSON, "image_config")
	if !imgCfg.Exists() || !imgCfg.IsObject() {
		return out
	}
	if ar := imgCfg.Get("aspect_ratio"); ar.Exists() && ar.Type == gjson.String {
		out, _ = sjson.SetBytes(out, "request.generationConfig.imageConfig.aspectRatio", ar.Str)
	}
	if size := imgCfg.Get("image_size"); size.Exists() && size.Type == gjson.String {
		out, _ = sjson.SetBytes(out, "request.generationConfig.imageConfig.imageSize", size.Str)
	}
	return out
}

// stage2ExtractAntigravityOpenAIMessages processes chat completion messages into
// system instructions and conversation contents.
func stage2ExtractAntigravityOpenAIMessages(out, rawJSON []byte, functionNameMap map[string]string) []byte {
	messages := gjson.GetBytes(rawJSON, "messages")
	if !messages.IsArray() {
		return out
	}
	arr := messages.Array()
	if len(arr) == 0 {
		return out
	}

	tcID2Name, toolResponses := buildOpenAIToolCallMaps(arr)
	systemParts := make([][]byte, 0, 2)
	contentItems := make([][]byte, 0, len(arr))
	hasEncounteredConversation := false

	for i := 0; i < len(arr); i++ {
		m := arr[i]
		role := m.Get("role").String()
		content := m.Get("content")

		if (role == "system" || role == "developer") && len(arr) > 1 && !hasEncounteredConversation {
			systemParts = extractOpenAISystemParts(content, systemParts)
		} else if role == "user" || role == "system" || role == "developer" {
			hasEncounteredConversation = true
			if userContent := processOpenAIUserMessage(content); len(userContent) > 0 {
				contentItems = append(contentItems, userContent)
			}
		} else if role == "assistant" {
			hasEncounteredConversation = true
			contentItems = processOpenAIAssistantTurn(m, content, functionNameMap, tcID2Name, toolResponses, contentItems)
		}
	}

	if len(systemParts) > 0 {
		out, _ = sjson.SetRawBytes(out, "request.systemInstruction", antigravityOpenAIContent("user", systemParts))
	}
	return translatorcommon.SetRawArrayItems(out, "request.contents", contentItems)
}

func buildOpenAIToolCallMaps(arr []gjson.Result) (map[string]string, map[string]string) {
	tcID2Name := make(map[string]string)
	toolResponses := make(map[string]string)

	for i := 0; i < len(arr); i++ {
		m := arr[i]
		role := m.Get("role").String()
		if role == "assistant" {
			tcs := m.Get("tool_calls")
			if tcs.IsArray() {
				for _, tc := range tcs.Array() {
					if tc.Get("type").String() == "function" {
						id := tc.Get("id").String()
						name := tc.Get("function.name").String()
						if id != "" && name != "" {
							tcID2Name[id] = name
						}
					}
				}
			}
		} else if role == "tool" {
			toolCallID := m.Get("tool_call_id").String()
			if toolCallID != "" {
				toolResponses[toolCallID] = m.Get("content").String()
			}
		}
	}
	return tcID2Name, toolResponses
}

func extractOpenAISystemParts(content gjson.Result, systemParts [][]byte) [][]byte {
	if content.Type == gjson.String {
		return append(systemParts, antigravityOpenAITextPart(content.String()))
	}
	if content.IsObject() && content.Get("type").String() == "text" {
		return append(systemParts, antigravityOpenAITextPart(content.Get("text").String()))
	}
	if content.IsArray() {
		for _, contentPart := range content.Array() {
			systemParts = append(systemParts, antigravityOpenAITextPart(contentPart.Get("text").String()))
		}
	}
	return systemParts
}

func processOpenAIUserMessage(content gjson.Result) []byte {
	partItems := make([][]byte, 0, 4)
	if content.Type == gjson.String {
		partItems = append(partItems, antigravityOpenAITextPart(content.String()))
	} else if content.IsObject() && content.Get("type").String() == "text" {
		partItems = append(partItems, antigravityOpenAITextPart(content.Get("text").String()))
	} else if content.IsArray() {
		for _, item := range content.Array() {
			partItems = processOpenAIUserContentItem(item, partItems)
		}
	}
	if len(partItems) == 0 {
		return nil
	}
	return antigravityOpenAIContent("user", partItems)
}

func processOpenAIUserContentItem(item gjson.Result, partItems [][]byte) [][]byte {
	switch item.Get("type").String() {
	case "text":
		if text := item.Get("text").String(); text != "" {
			partItems = append(partItems, antigravityOpenAITextPart(text))
		}
	case "image_url":
		imageURL := item.Get("image_url.url").String()
		if strings.HasPrefix(imageURL, "data:") {
			pieces := strings.SplitN(imageURL[5:], ";", 2)
			if len(pieces) == 2 && strings.HasPrefix(pieces[1], "base64,") {
				part := antigravityOpenAIInlineDataPart(pieces[0], pieces[1][7:], false)
				part, _ = sjson.SetBytes(part, "thoughtSignature", antigravityFunctionThoughtSignature)
				partItems = append(partItems, part)
			}
		}
	case "video_url":
		videoURL := item.Get("video_url.url").String()
		if strings.HasPrefix(videoURL, "data:") {
			pieces := strings.SplitN(videoURL[5:], ";", 2)
			if len(pieces) == 2 && strings.HasPrefix(pieces[1], "base64,") {
				partItems = append(partItems, antigravityOpenAIInlineDataPart(pieces[0], pieces[1][7:], false))
			}
		}
	case "file":
		filename := item.Get("file.filename").String()
		fileData := item.Get("file.file_data").String()
		if mimeType, data, ok := translatorcommon.NormalizeOpenAIFileData(filename, "", fileData); ok {
			partItems = append(partItems, antigravityOpenAIInlineDataPart(mimeType, data, false))
		} else {
			log.Warn("Invalid file data or unknown file name extension in user message, skip")
		}
	case "input_audio":
		audioData := item.Get("input_audio.data").String()
		if audioData != "" {
			mimeType := antigravityOpenAIAudioMIMEType(item.Get("input_audio.format").String())
			partItems = append(partItems, antigravityOpenAIInlineDataPart(mimeType, audioData, true))
		}
	}
	return partItems
}

func processOpenAIAssistantTurn(
	m gjson.Result,
	content gjson.Result,
	functionNameMap map[string]string,
	tcID2Name map[string]string,
	toolResponses map[string]string,
	contentItems [][]byte,
) [][]byte {
	partItems := make([][]byte, 0, 4)
	if reasoningContent := m.Get("reasoning_content"); reasoningContent.Type == gjson.String && reasoningContent.String() != "" {
		part := antigravityOpenAITextPart(reasoningContent.String())
		part, _ = sjson.SetBytes(part, "thought", true)
		part, _ = sjson.SetBytes(part, "thoughtSignature", antigravityOpenAIReasoningContentThoughtSignature(m))
		partItems = append(partItems, part)
	}

	if content.Type == gjson.String && content.String() != "" {
		partItems = append(partItems, antigravityOpenAITextPart(content.String()))
	} else if content.IsArray() {
		for _, item := range content.Array() {
			switch item.Get("type").String() {
			case "text":
				if text := item.Get("text").String(); text != "" {
					partItems = append(partItems, antigravityOpenAITextPart(text))
				}
			case "image_url":
				imageURL := item.Get("image_url.url").String()
				if len(imageURL) > 5 {
					pieces := strings.SplitN(imageURL[5:], ";", 2)
					if len(pieces) == 2 && len(pieces[1]) > 7 {
						part := antigravityOpenAIInlineDataPart(pieces[0], pieces[1][7:], false)
						part, _ = sjson.SetBytes(part, "thoughtSignature", antigravityFunctionThoughtSignature)
						partItems = append(partItems, part)
					}
				}
			}
		}
	}

	tcs := m.Get("tool_calls")
	if tcs.IsArray() {
		var functionIDs []string
		partItems, functionIDs = appendOpenAIFunctionCalls(tcs.Array(), m, functionNameMap, partItems)
		if len(partItems) > 0 {
			contentItems = append(contentItems, antigravityOpenAIContent("model", partItems))
		}
		if responseContent := buildOpenAIFunctionResponses(functionIDs, tcID2Name, toolResponses, functionNameMap); len(responseContent) > 0 {
			contentItems = append(contentItems, responseContent)
		}
	} else if len(partItems) > 0 {
		contentItems = append(contentItems, antigravityOpenAIContent("model", partItems))
	}
	return contentItems
}

func appendOpenAIFunctionCalls(
	toolCalls []gjson.Result,
	m gjson.Result,
	functionNameMap map[string]string,
	partItems [][]byte,
) ([][]byte, []string) {
	functionIDs := make([]string, 0, len(toolCalls))
	for _, tc := range toolCalls {
		if tc.Get("type").String() != "function" {
			continue
		}
		functionID := tc.Get("id").String()
		functionName := util.MapSanitizedFunctionName(functionNameMap, tc.Get("function.name").String())
		if functionName == "" {
			continue
		}
		functionArgs := tc.Get("function.arguments").String()
		part := []byte(`{"functionCall":{"id":"","name":""}}`)
		part, _ = sjson.SetBytes(part, "functionCall.id", functionID)
		part, _ = sjson.SetBytes(part, "functionCall.name", functionName)
		if gjson.Valid(functionArgs) {
			part, _ = sjson.SetRawBytes(part, "functionCall.args", []byte(functionArgs))
		} else {
			part, _ = sjson.SetBytes(part, "functionCall.args.params", []byte(functionArgs))
		}
		part, _ = sjson.SetBytes(part, "thoughtSignature", antigravityOpenAIToolCallThoughtSignature(tc, m))
		partItems = append(partItems, part)
		if functionID != "" {
			functionIDs = append(functionIDs, functionID)
		}
	}
	return partItems, functionIDs
}

func buildOpenAIFunctionResponses(
	functionIDs []string,
	tcID2Name map[string]string,
	toolResponses map[string]string,
	functionNameMap map[string]string,
) []byte {
	responseParts := make([][]byte, 0, len(functionIDs))
	for _, functionID := range functionIDs {
		if name, ok := tcID2Name[functionID]; ok {
			part := []byte(`{"functionResponse":{"id":"","name":""}}`)
			part, _ = sjson.SetBytes(part, "functionResponse.id", functionID)
			part, _ = sjson.SetBytes(part, "functionResponse.name", util.MapSanitizedFunctionName(functionNameMap, name))
			response := toolResponses[functionID]
			if response == "" {
				response = "{}"
			}
			part, _ = sjson.SetBytes(part, "functionResponse.response.result", response)
			responseParts = append(responseParts, part)
		}
	}
	if len(responseParts) == 0 {
		return nil
	}
	return antigravityOpenAIContent("user", responseParts)
}

// stage3NormalizeAntigravityOpenAITools processes OpenAI tools declarations into Antigravity function declarations
// and handles built-in tools passthrough (googleSearch, codeExecution, urlContext).
func stage3NormalizeAntigravityOpenAITools(out, rawJSON []byte, functionNameMap map[string]string) []byte {
	tools := gjson.GetBytes(rawJSON, "tools")
	if !tools.IsArray() {
		return out
	}
	toolResults := tools.Array()
	if len(toolResults) == 0 {
		return out
	}

	functionDeclarations := make([][]byte, 0, len(toolResults))
	googleSearchNodes := make([][]byte, 0)
	codeExecutionNodes := make([][]byte, 0)
	urlContextNodes := make([][]byte, 0)

	for _, t := range toolResults {
		if t.Get("type").String() == "function" {
			if fnDecl := formatOpenAIFunctionDeclaration(t.Get("function"), functionNameMap); len(fnDecl) > 0 {
				functionDeclarations = append(functionDeclarations, fnDecl)
			}
		}
		if gs := t.Get("google_search"); gs.Exists() {
			if node := buildAntigravityToolNode("googleSearch", gs.Raw); len(node) > 0 {
				googleSearchNodes = append(googleSearchNodes, node)
			}
		}
		if ce := t.Get("code_execution"); ce.Exists() {
			if node := buildAntigravityToolNode("codeExecution", ce.Raw); len(node) > 0 {
				codeExecutionNodes = append(codeExecutionNodes, node)
			}
		}
		if uc := t.Get("url_context"); uc.Exists() {
			if node := buildAntigravityToolNode("urlContext", uc.Raw); len(node) > 0 {
				urlContextNodes = append(urlContextNodes, node)
			}
		}
	}

	deduplicated := util.DeduplicateFunctionDeclarations(translatorcommon.JoinRawArray(functionDeclarations))
	hasFunction := len(deduplicated) > 2
	if hasFunction || len(googleSearchNodes) > 0 || len(codeExecutionNodes) > 0 || len(urlContextNodes) > 0 {
		toolItems := make([][]byte, 0, 1+len(googleSearchNodes)+len(codeExecutionNodes)+len(urlContextNodes))
		if hasFunction {
			functionToolNode := []byte(`{"functionDeclarations":[]}`)
			functionToolNode, _ = sjson.SetRawBytes(functionToolNode, "functionDeclarations", deduplicated)
			toolItems = append(toolItems, functionToolNode)
		}
		toolItems = append(toolItems, googleSearchNodes...)
		toolItems = append(toolItems, codeExecutionNodes...)
		toolItems = append(toolItems, urlContextNodes...)
		out, _ = sjson.SetRawBytes(out, "request.tools", translatorcommon.JoinRawArray(toolItems))
	}
	return out
}

func formatOpenAIFunctionDeclaration(fn gjson.Result, functionNameMap map[string]string) []byte {
	if !fn.Exists() || !fn.IsObject() {
		return nil
	}
	fnRaw := fn.Raw
	if fn.Get("parameters").Exists() {
		renamed, errRename := util.RenameKey(fnRaw, "parameters", "parametersJsonSchema")
		if errRename != nil {
			log.Warnf("Failed to rename parameters for tool '%s': %v", fn.Get("name").String(), errRename)
			fnRaw = setDefaultSchemaFallback(fnRaw, fn.Get("name").String())
			if fnRaw == "" {
				return nil
			}
		} else {
			fnRaw = renamed
		}
	} else {
		fnRaw = setDefaultSchemaFallback(fnRaw, fn.Get("name").String())
		if fnRaw == "" {
			return nil
		}
	}

	fnRawBytes := []byte(fnRaw)
	nameResult := fn.Get("name")
	originalName := nameResult.String()
	mappedName := util.MapSanitizedFunctionName(functionNameMap, originalName)
	if nameResult.Type != gjson.String || mappedName != originalName {
		fnRawBytes, _ = sjson.SetBytes(fnRawBytes, "name", mappedName)
	}
	if gjson.GetBytes(fnRawBytes, "strict").Exists() {
		fnRawBytes, _ = sjson.DeleteBytes(fnRawBytes, "strict")
	}
	return fnRawBytes
}

func setDefaultSchemaFallback(fnRaw, fnName string) string {
	fnRawBytes := []byte(fnRaw)
	if gjson.GetBytes(fnRawBytes, "parameters").Exists() {
		fnRawBytes, _ = sjson.DeleteBytes(fnRawBytes, "parameters")
	}
	var errSet error
	fnRawBytes, errSet = sjson.SetBytes(fnRawBytes, "parametersJsonSchema.type", "object")
	if errSet != nil {
		log.Warnf("Failed to set default schema type for tool '%s': %v", fnName, errSet)
		return ""
	}
	fnRawBytes, errSet = sjson.SetRawBytes(fnRawBytes, "parametersJsonSchema.properties", []byte(`{}`))
	if errSet != nil {
		log.Warnf("Failed to set default schema properties for tool '%s': %v", fnName, errSet)
		return ""
	}
	return string(fnRawBytes)
}

func buildAntigravityToolNode(key, rawVal string) []byte {
	node := []byte(`{}`)
	node, err := sjson.SetRawBytes(node, key, []byte(rawVal))
	if err != nil {
		log.Warnf("Failed to set %s tool: %v", key, err)
		return nil
	}
	return node
}

// stage4FinalizeAntigravityOpenAIPayload applies tool choice configuration, cleanses Claude signatures
// if applicable, and attaches standard safety settings.
func stage4FinalizeAntigravityOpenAIPayload(out []byte, modelName string, rawJSON []byte, functionNameMap map[string]string) []byte {
	out = applyOpenAIToolChoiceToAntigravity(out, rawJSON, functionNameMap)
	if strings.Contains(strings.ToLower(modelName), "claude") {
		out = gemini.SanitizeAntigravityClaudeGeminiRequestSignatures(modelName, out)
	}
	return common.AttachDefaultSafetySettings(out, "request.safetySettings")
}

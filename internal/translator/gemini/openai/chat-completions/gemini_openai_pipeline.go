package chat_completions

import (
	"strings"

	translatorcommon "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/common"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/translator/gemini/common"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// =========================================================================
// STAGE 1: Envelope & Sampling Configuration
// =========================================================================

func applyOpenAIToGeminiEnvelope(modelName string, rawJSON []byte) []byte {
	out := []byte(`{"contents":[]}`)
	out, _ = sjson.SetBytes(out, "model", modelName)

	if genConfig := gjson.GetBytes(rawJSON, "generationConfig"); genConfig.Exists() {
		out, _ = sjson.SetRawBytes(out, "generationConfig", []byte(genConfig.Raw))
	}

	out = applyOpenAIThinkingToGemini(out, rawJSON)
	out = applyOpenAISamplingToGemini(out, rawJSON)
	out = applyOpenAIMaxTokensToGemini(out, rawJSON)
	out = applyOpenAICandidateCountToGemini(out, rawJSON)
	out = applyOpenAIResponseFormatToGemini(out, rawJSON)
	out = applyOpenAIModalitiesAndImageConfig(out, rawJSON)

	return out
}

func applyOpenAIThinkingToGemini(out, rawJSON []byte) []byte {
	re := gjson.GetBytes(rawJSON, "reasoning_effort")
	if !re.Exists() {
		return out
	}
	effort := strings.ToLower(strings.TrimSpace(re.String()))
	if effort == "" {
		return out
	}
	thinkingPath := "generationConfig.thinkingConfig"
	if effort == "auto" {
		out, _ = sjson.SetBytes(out, thinkingPath+".thinkingBudget", -1)
	} else {
		out, _ = sjson.SetBytes(out, thinkingPath+".thinkingLevel", effort)
	}
	return out
}

func applyOpenAISamplingToGemini(out, rawJSON []byte) []byte {
	if tr := gjson.GetBytes(rawJSON, "temperature"); tr.Exists() && tr.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "generationConfig.temperature", tr.Num)
	}
	if tpr := gjson.GetBytes(rawJSON, "top_p"); tpr.Exists() && tpr.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "generationConfig.topP", tpr.Num)
	}
	if tkr := gjson.GetBytes(rawJSON, "top_k"); tkr.Exists() && tkr.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "generationConfig.topK", tkr.Num)
	}
	return out
}

func applyOpenAIMaxTokensToGemini(out, rawJSON []byte) []byte {
	if mt := gjson.GetBytes(rawJSON, "max_tokens"); mt.Exists() && mt.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "generationConfig.maxOutputTokens", mt.Num)
	} else if mct := gjson.GetBytes(rawJSON, "max_completion_tokens"); mct.Exists() && mct.Type == gjson.Number {
		out, _ = sjson.SetBytes(out, "generationConfig.maxOutputTokens", mct.Num)
	}
	return out
}

func applyOpenAICandidateCountToGemini(out, rawJSON []byte) []byte {
	if n := gjson.GetBytes(rawJSON, "n"); n.Exists() && n.Type == gjson.Number {
		if val := n.Int(); val > 1 {
			out, _ = sjson.SetBytes(out, "generationConfig.candidateCount", val)
		}
	}
	return out
}

func applyOpenAIModalitiesAndImageConfig(out, rawJSON []byte) []byte {
	if mods := gjson.GetBytes(rawJSON, "modalities"); mods.Exists() && mods.IsArray() {
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
			out, _ = sjson.SetBytes(out, "generationConfig.responseModalities", responseMods)
		}
	}

	if imgCfg := gjson.GetBytes(rawJSON, "image_config"); imgCfg.Exists() && imgCfg.IsObject() {
		if ar := imgCfg.Get("aspect_ratio"); ar.Exists() && ar.Type == gjson.String {
			out, _ = sjson.SetBytes(out, "generationConfig.imageConfig.aspectRatio", ar.Str)
		}
		if size := imgCfg.Get("image_size"); size.Exists() && size.Type == gjson.String {
			out, _ = sjson.SetBytes(out, "generationConfig.imageConfig.imageSize", size.Str)
		}
	}
	return out
}

// =========================================================================
// STAGE 2: Messages & Multimodal Transformation
// =========================================================================

func applyOpenAIToGeminiMessages(out, rawJSON []byte) []byte {
	messages := gjson.GetBytes(rawJSON, "messages")
	if !messages.IsArray() {
		return out
	}
	arr := messages.Array()
	if len(arr) == 0 {
		return out
	}

	tcID2Name, toolResponses := buildToolCaches(arr)
	systemParts, contentItems := transformMessagesList(arr, tcID2Name, toolResponses)

	if len(systemParts) > 0 {
		systemInstruction := geminiContentNode("user", systemParts)
		out, _ = sjson.SetRawBytes(out, "systemInstruction", systemInstruction)
	}
	if len(contentItems) > 0 && gjson.GetBytes(contentItems[len(contentItems)-1], "role").String() == "model" {
		contentItems = contentItems[:len(contentItems)-1]
	}
	return translatorcommon.SetRawArrayItems(out, "contents", contentItems)
}

func buildToolCaches(arr []gjson.Result) (map[string]string, map[string]string) {
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
				toolResponses[toolCallID] = m.Get("content").Raw
			}
		}
	}
	return tcID2Name, toolResponses
}

func transformMessagesList(
	arr []gjson.Result,
	tcID2Name, toolResponses map[string]string,
) ([][]byte, [][]byte) {
	systemParts := make([][]byte, 0, 2)
	contentItems := make([][]byte, 0, len(arr))
	hasEncounteredConversation := false

	for i := 0; i < len(arr); i++ {
		m := arr[i]
		role := m.Get("role").String()
		content := m.Get("content")

		if (role == "system" || role == "developer") && len(arr) > 1 && !hasEncounteredConversation {
			systemParts = append(systemParts, extractSystemParts(content)...)
			continue
		}

		if role == "user" || role == "system" || role == "developer" {
			hasEncounteredConversation = true
			if userNode := buildUserContentNode(content); len(userNode) > 0 {
				contentItems = append(contentItems, userNode)
			}
			continue
		}

		if role == "assistant" {
			hasEncounteredConversation = true
			assistantNodes := buildAssistantContentNodes(m, tcID2Name, toolResponses)
			contentItems = append(contentItems, assistantNodes...)
		}
	}

	return systemParts, contentItems
}

func extractSystemParts(content gjson.Result) [][]byte {
	var parts [][]byte
	if content.Type == gjson.String {
		parts = append(parts, geminiTextPart(content.String()))
	} else if content.IsObject() && content.Get("type").String() == "text" {
		parts = append(parts, geminiTextPart(content.Get("text").String()))
	} else if content.IsArray() {
		for _, item := range content.Array() {
			parts = append(parts, geminiTextPart(item.Get("text").String()))
		}
	}
	return parts
}

func buildUserContentNode(content gjson.Result) []byte {
	partItems := make([][]byte, 0, 4)
	if content.Type == gjson.String {
		partItems = append(partItems, geminiTextPart(content.String()))
	} else if content.IsObject() && content.Get("type").String() == "text" {
		partItems = append(partItems, geminiTextPart(content.Get("text").String()))
	} else if content.IsArray() {
		for _, item := range content.Array() {
			if part := extractUserMultimodalPart(item); len(part) > 0 {
				partItems = append(partItems, part)
			}
		}
	}
	if len(partItems) == 0 {
		return nil
	}
	return geminiContentNode("user", partItems)
}

func extractUserMultimodalPart(item gjson.Result) []byte {
	switch item.Get("type").String() {
	case "text":
		if text := item.Get("text").String(); text != "" {
			return geminiTextPart(text)
		}
	case "image_url":
		return parseDataURLPart(item.Get("image_url.url").String(), geminiFunctionThoughtSignature)
	case "video_url":
		return parseDataURLPart(item.Get("video_url.url").String(), "")
	case "file":
		filename := item.Get("file.filename").String()
		fileData := item.Get("file.file_data").String()
		if mimeType, data, ok := translatorcommon.NormalizeOpenAIFileData(filename, "", fileData); ok {
			return geminiInlineDataPart(mimeType, data, "")
		}
		log.Warn("Invalid file data or unknown file name extension in user message, skip")
	case "input_audio":
		audioData := item.Get("input_audio.data").String()
		if audioData != "" {
			mimeType := openAIInputAudioMimeType(item.Get("input_audio.format").String())
			return geminiInlineDataPart(mimeType, audioData, "")
		}
	}
	return nil
}

func parseDataURLPart(url, signature string) []byte {
	if len(url) > 5 && strings.HasPrefix(url, "data:") {
		pieces := strings.SplitN(url[5:], ";", 2)
		if len(pieces) == 2 && strings.HasPrefix(pieces[1], "base64,") {
			return geminiInlineDataPart(pieces[0], pieces[1][7:], signature)
		}
	}
	return nil
}

func buildAssistantContentNodes(
	m gjson.Result,
	tcID2Name, toolResponses map[string]string,
) [][]byte {
	var nodes [][]byte
	partItems := make([][]byte, 0, 4)

	if reasoningContent := m.Get("reasoning_content"); reasoningContent.Type == gjson.String && reasoningContent.String() != "" {
		part := geminiTextPart(reasoningContent.String())
		part, _ = sjson.SetBytes(part, "thought", true)
		part, _ = sjson.SetBytes(part, "thoughtSignature", geminiFunctionThoughtSignature)
		partItems = append(partItems, part)
	}

	content := m.Get("content")
	if content.Type == gjson.String && content.String() != "" {
		partItems = append(partItems, geminiTextPart(content.String()))
	} else if content.IsArray() {
		for _, item := range content.Array() {
			if item.Get("type").String() == "text" {
				if text := item.Get("text").String(); text != "" {
					partItems = append(partItems, geminiTextPart(text))
				}
			} else if item.Get("type").String() == "image_url" {
				if part := parseDataURLPart(item.Get("image_url.url").String(), geminiFunctionThoughtSignature); len(part) > 0 {
					partItems = append(partItems, part)
				}
			}
		}
	}

	tcs := m.Get("tool_calls")
	if !tcs.IsArray() {
		if len(partItems) > 0 {
			nodes = append(nodes, geminiContentNode("model", partItems))
		}
		return nodes
	}

	callParts, functionIDs := extractAssistantToolCalls(tcs.Array())
	partItems = append(partItems, callParts...)
	if len(partItems) > 0 {
		nodes = append(nodes, geminiContentNode("model", partItems))
	}

	if responseNode := buildToolResponseNode(functionIDs, tcID2Name, toolResponses); len(responseNode) > 0 {
		nodes = append(nodes, responseNode)
	}

	return nodes
}

func extractAssistantToolCalls(tcArray []gjson.Result) ([][]byte, []string) {
	callParts := make([][]byte, 0, len(tcArray))
	functionIDs := make([]string, 0, len(tcArray))

	for _, tc := range tcArray {
		if tc.Get("type").String() != "function" {
			continue
		}
		functionName := util.SanitizeFunctionName(tc.Get("function.name").String())
		if functionName == "" {
			continue
		}
		part := []byte(`{"functionCall":{"name":""}}`)
		part, _ = sjson.SetBytes(part, "functionCall.name", functionName)
		part, _ = sjson.SetRawBytes(part, "functionCall.args", []byte(tc.Get("function.arguments").String()))
		part, _ = sjson.SetBytes(part, "thoughtSignature", openAIToolCallGeminiThoughtSignature(tc))
		callParts = append(callParts, part)

		if functionID := tc.Get("id").String(); functionID != "" {
			functionIDs = append(functionIDs, functionID)
		}
	}
	return callParts, functionIDs
}

func buildToolResponseNode(
	functionIDs []string,
	tcID2Name, toolResponses map[string]string,
) []byte {
	responseParts := make([][]byte, 0, len(functionIDs))
	for _, functionID := range functionIDs {
		name, ok := tcID2Name[functionID]
		if !ok {
			continue
		}
		part := []byte(`{"functionResponse":{"name":"","response":{"result":""}}}`)
		part, _ = sjson.SetBytes(part, "functionResponse.name", util.SanitizeFunctionName(name))
		response := toolResponses[functionID]
		if response == "" {
			response = "{}"
		}
		part, _ = sjson.SetBytes(part, "functionResponse.response.result", []byte(response))
		responseParts = append(responseParts, part)
	}
	if len(responseParts) == 0 {
		return nil
	}
	return geminiContentNode("user", responseParts)
}

// =========================================================================
// STAGE 3: Tools & Builtin Services Normalization
// =========================================================================

func applyOpenAIToGeminiTools(out, rawJSON []byte) []byte {
	tools := gjson.GetBytes(rawJSON, "tools")
	toolResults := tools.Array()
	if !tools.IsArray() || len(toolResults) == 0 {
		return out
	}

	functionDeclarations := make([][]byte, 0, len(toolResults))
	googleSearchNodes := make([][]byte, 0)
	codeExecutionNodes := make([][]byte, 0)
	urlContextNodes := make([][]byte, 0)

	for _, t := range toolResults {
		if t.Get("type").String() == "function" {
			if fnDecl := normalizeGeminiFunctionTool(t.Get("function")); len(fnDecl) > 0 {
				functionDeclarations = append(functionDeclarations, fnDecl)
			}
		}
		if gs := t.Get("google_search"); gs.Exists() {
			if node := buildToolServiceNode("googleSearch", gs.Raw); len(node) > 0 {
				googleSearchNodes = append(googleSearchNodes, node)
			}
		}
		if ce := t.Get("code_execution"); ce.Exists() {
			if node := buildToolServiceNode("codeExecution", ce.Raw); len(node) > 0 {
				codeExecutionNodes = append(codeExecutionNodes, node)
			}
		}
		if uc := t.Get("url_context"); uc.Exists() {
			if node := buildToolServiceNode("urlContext", uc.Raw); len(node) > 0 {
				urlContextNodes = append(urlContextNodes, node)
			}
		}
	}

	if len(functionDeclarations) == 0 && len(googleSearchNodes) == 0 && len(codeExecutionNodes) == 0 && len(urlContextNodes) == 0 {
		return out
	}

	toolItems := make([][]byte, 0, 1+len(googleSearchNodes)+len(codeExecutionNodes)+len(urlContextNodes))
	if len(functionDeclarations) > 0 {
		functionToolNode := []byte(`{"functionDeclarations":[]}`)
		functionToolNode, _ = sjson.SetRawBytes(functionToolNode, "functionDeclarations", translatorcommon.JoinRawArray(functionDeclarations))
		toolItems = append(toolItems, functionToolNode)
	}
	toolItems = append(toolItems, googleSearchNodes...)
	toolItems = append(toolItems, codeExecutionNodes...)
	toolItems = append(toolItems, urlContextNodes...)

	out, _ = sjson.SetRawBytes(out, "tools", translatorcommon.JoinRawArray(toolItems))
	return out
}

func normalizeGeminiFunctionTool(fn gjson.Result) []byte {
	if !fn.Exists() || !fn.IsObject() {
		return nil
	}
	fnRaw := fn.Raw
	toolName := fn.Get("name").String()

	if fn.Get("parameters").Exists() {
		renamed, errRename := util.RenameKey(fnRaw, "parameters", "parametersJsonSchema")
		if errRename != nil {
			log.Warnf("Failed to rename parameters for tool '%s': %v", toolName, errRename)
			fallback, ok := setToolDefaultSchema(fnRaw, toolName)
			if !ok {
				return nil
			}
			fnRaw = fallback
		} else {
			fnRaw = renamed
		}
	} else {
		fallback, ok := setToolDefaultSchema(fnRaw, toolName)
		if !ok {
			return nil
		}
		fnRaw = fallback
	}

	fnRawBytes := []byte(fnRaw)
	nameResult := fn.Get("name")
	originalName := nameResult.String()
	sanitizedName := util.SanitizeFunctionName(originalName)
	if nameResult.Type != gjson.String || sanitizedName != originalName {
		fnRawBytes, _ = sjson.SetBytes(fnRawBytes, "name", sanitizedName)
	}

	if parameters := gjson.GetBytes(fnRawBytes, "parametersJsonSchema"); parameters.Exists() {
		cleanedParameters := util.CleanJSONSchemaForGemini(parameters.Raw)
		if cleanedParameters != parameters.Raw {
			fnRawBytes, _ = sjson.SetRawBytes(fnRawBytes, "parametersJsonSchema", []byte(cleanedParameters))
		}
	}

	if gjson.GetBytes(fnRawBytes, "strict").Exists() {
		fnRawBytes, _ = sjson.DeleteBytes(fnRawBytes, "strict")
	}

	return fnRawBytes
}

func setToolDefaultSchema(fnRaw, toolName string) (string, bool) {
	fnRawBytes := []byte(fnRaw)
	var errSet error
	fnRawBytes, errSet = sjson.SetBytes(fnRawBytes, "parametersJsonSchema.type", "object")
	if errSet != nil {
		log.Warnf("Failed to set default schema type for tool '%s': %v", toolName, errSet)
		return "", false
	}
	fnRawBytes, errSet = sjson.SetRawBytes(fnRawBytes, "parametersJsonSchema.properties", []byte(`{}`))
	if errSet != nil {
		log.Warnf("Failed to set default schema properties for tool '%s': %v", toolName, errSet)
		return "", false
	}
	return string(fnRawBytes), true
}

func buildToolServiceNode(key, raw string) []byte {
	node := []byte(`{}`)
	node, err := sjson.SetRawBytes(node, key, []byte(raw))
	if err != nil {
		log.Warnf("Failed to set %s tool: %v", key, err)
		return nil
	}
	return node
}

// =========================================================================
// STAGE 4: Safety Settings & Finalization
// =========================================================================

func applyOpenAIToGeminiSafetySettings(out []byte) []byte {
	return common.AttachDefaultSafetySettings(out, "safetySettings")
}

// Package openai provides request translation functionality for OpenAI to Antigravity API compatibility.
// It converts OpenAI Chat Completions requests into Antigravity compatible JSON using gjson/sjson only.
package chat_completions

import (
	"strings"

	sigcompat "github.com/router-for-me/CLIProxyAPI/v7/internal/signature"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	translatorcommon "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/common"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const antigravityFunctionThoughtSignature = "skip_thought_signature_validator"

// ConvertOpenAIRequestToAntigravity converts an OpenAI Chat Completions request (raw JSON)
// into a complete Antigravity request JSON using a modular 4-stage pipeline.
//
// Parameters:
//   - modelName: The name of the model to use for the request
//   - inputRawJSON: The raw JSON request data from the OpenAI API
//   - stream: A boolean indicating if the request is for a streaming response (unused in current implementation)
//
// Returns:
//   - []byte: The transformed request data in Antigravity API format
func ConvertOpenAIRequestToAntigravity(modelName string, inputRawJSON []byte, _ bool) []byte {
	rawJSON := inputRawJSON
	functionNameMap := util.SanitizedFunctionNameMap(rawJSON)

	// Stage 1: Initialize envelope, model, generation config, thinking, sampling, structured output, modalities, and image config
	out := stage1InitAntigravityOpenAIEnvelope(modelName, rawJSON)

	// Stage 2: Extract system instructions and conversation contents (multimodal, tool calls, and tool responses)
	out = stage2ExtractAntigravityOpenAIMessages(out, rawJSON, functionNameMap)

	// Stage 3: Normalize function tools and passthrough tools (googleSearch, codeExecution, urlContext)
	out = stage3NormalizeAntigravityOpenAITools(out, rawJSON, functionNameMap)

	// Stage 4: Apply tool choice, model sanitization, and safety settings
	return stage4FinalizeAntigravityOpenAIPayload(out, modelName, rawJSON, functionNameMap)
}

func antigravityOpenAITextPart(text string) []byte {
	part := []byte(`{"text":""}`)
	part, _ = sjson.SetBytes(part, "text", text)
	return part
}

func antigravityOpenAIInlineDataPart(mimeType, data string, snakeCase bool) []byte {
	part := []byte(`{"inlineData":{"mimeType":"","data":""}}`)
	if snakeCase {
		part = []byte(`{"inlineData":{"mime_type":"","data":""}}`)
		part, _ = sjson.SetBytes(part, "inlineData.mime_type", mimeType)
	} else {
		part, _ = sjson.SetBytes(part, "inlineData.mimeType", mimeType)
	}
	part, _ = sjson.SetBytes(part, "inlineData.data", data)
	return part
}

func antigravityOpenAIContent(role string, parts [][]byte) []byte {
	content := []byte(`{"role":"","parts":[]}`)
	content, _ = sjson.SetBytes(content, "role", role)
	content, _ = sjson.SetRawBytes(content, "parts", translatorcommon.JoinRawArray(parts))
	return content
}

func antigravityOpenAIAudioMIMEType(format string) string {
	switch format {
	case "mp3":
		return "audio/mpeg"
	case "ogg":
		return "audio/ogg"
	case "flac":
		return "audio/flac"
	case "aac":
		return "audio/aac"
	case "webm":
		return "audio/webm"
	case "pcm16":
		return "audio/pcm"
	case "g711_ulaw", "g711_alaw":
		return "audio/basic"
	case "", "wav":
		return "audio/wav"
	default:
		return "audio/" + format
	}
}

func applyOpenAIToolChoiceToAntigravity(out, rawJSON []byte, functionNameMap map[string]string) []byte {
	toolChoice := gjson.GetBytes(rawJSON, "tool_choice")
	if !toolChoice.Exists() {
		return out
	}

	mode := ""
	allowedName := ""
	if toolChoice.Type == gjson.String {
		switch strings.ToLower(strings.TrimSpace(toolChoice.String())) {
		case "none":
			mode = "NONE"
		case "auto":
			mode = "AUTO"
		case "required", "any":
			mode = "ANY"
		}
	} else if toolChoice.IsObject() {
		switch strings.ToLower(strings.TrimSpace(toolChoice.Get("type").String())) {
		case "none":
			mode = "NONE"
		case "function":
			mode = "ANY"
			allowedName = toolChoice.Get("function.name").String()
		}
	}
	if mode == "" {
		return out
	}

	out, _ = sjson.SetBytes(out, "request.toolConfig.functionCallingConfig.mode", mode)
	if mode == "NONE" {
		out, _ = sjson.DeleteBytes(out, "request.tools")
	}
	if strings.TrimSpace(allowedName) != "" {
		mappedName := util.MapSanitizedFunctionName(functionNameMap, allowedName)
		out, _ = sjson.SetBytes(out, "request.toolConfig.functionCallingConfig.allowedFunctionNames", []string{mappedName})
	}
	return out
}

func applyOpenAIThinkingCompatibilityToAntigravity(out []byte, rawJSON []byte) []byte {
	out = normalizeAntigravityOpenAIThinkingConfig(out)
	config := thinking.ExtractSummaryConfig(rawJSON, "openai")
	return thinking.ApplySummaryConfig(out, "antigravity", config)
}

func normalizeAntigravityOpenAIThinkingConfig(out []byte) []byte {
	for _, prefix := range []string{
		"request.generationConfig.thinking_config",
		"request.generationConfig.thinkingConfig",
	} {
		if sourcePath := prefix + ".includeThoughts"; gjson.GetBytes(out, sourcePath).Exists() {
			includeThoughts := gjson.GetBytes(out, sourcePath)
			out = setAntigravityOpenAIBoolResultIfValid(out, "request.generationConfig.thinkingConfig.includeThoughts", includeThoughts)
			if includeThoughts.Type != gjson.True && includeThoughts.Type != gjson.False {
				out, _ = sjson.DeleteBytes(out, sourcePath)
			}
		}
		if sourcePath := prefix + ".include_thoughts"; gjson.GetBytes(out, sourcePath).Exists() {
			includeThoughts := gjson.GetBytes(out, sourcePath)
			out = setAntigravityOpenAIBoolResultIfValid(out, "request.generationConfig.thinkingConfig.includeThoughts", includeThoughts)
			if includeThoughts.Type != gjson.True && includeThoughts.Type != gjson.False {
				out, _ = sjson.DeleteBytes(out, sourcePath)
			}
		}
		if thinkingLevel := gjson.GetBytes(out, prefix+".thinkingLevel"); thinkingLevel.Exists() {
			out = setAntigravityOpenAIRawIfDifferent(out, "request.generationConfig.thinkingConfig.thinkingLevel", thinkingLevel)
		}
		if thinkingLevel := gjson.GetBytes(out, prefix+".thinking_level"); thinkingLevel.Exists() {
			out = setAntigravityOpenAIRawIfDifferent(out, "request.generationConfig.thinkingConfig.thinkingLevel", thinkingLevel)
		}
		if thinkingBudget := gjson.GetBytes(out, prefix+".thinkingBudget"); thinkingBudget.Exists() {
			out = setAntigravityOpenAIRawIfDifferent(out, "request.generationConfig.thinkingConfig.thinkingBudget", thinkingBudget)
		}
		if thinkingBudget := gjson.GetBytes(out, prefix+".thinking_budget"); thinkingBudget.Exists() {
			out = setAntigravityOpenAIRawIfDifferent(out, "request.generationConfig.thinkingConfig.thinkingBudget", thinkingBudget)
		}
	}

	for _, path := range []string{
		"request.generationConfig.includeThoughts",
		"request.generationConfig.include_thoughts",
	} {
		if includeThoughts := gjson.GetBytes(out, path); includeThoughts.Exists() {
			out = setAntigravityOpenAIBoolResultIfValid(out, "request.generationConfig.thinkingConfig.includeThoughts", includeThoughts)
		}
	}

	for _, path := range []string{
		"request.generationConfig.thinking_config",
		"request.generationConfig.thinkingConfig.include_thoughts",
		"request.generationConfig.thinkingConfig.thinking_level",
		"request.generationConfig.thinkingConfig.thinking_budget",
		"request.generationConfig.includeThoughts",
		"request.generationConfig.include_thoughts",
	} {
		if gjson.GetBytes(out, path).Exists() {
			out, _ = sjson.DeleteBytes(out, path)
		}
	}

	return out
}

func setAntigravityOpenAIBoolResultIfValid(out []byte, path string, value gjson.Result) []byte {
	switch value.Type {
	case gjson.True:
		return setAntigravityOpenAIBoolIfDifferent(out, path, true)
	case gjson.False:
		return setAntigravityOpenAIBoolIfDifferent(out, path, false)
	default:
		return out
	}
}

func setAntigravityOpenAIBoolIfDifferent(out []byte, path string, value bool) []byte {
	current := gjson.GetBytes(out, path)
	if value && current.Type == gjson.True || !value && current.Type == gjson.False {
		return out
	}
	updated, errSet := sjson.SetBytes(out, path, value)
	if errSet != nil {
		return out
	}
	return updated
}

func setAntigravityOpenAIRawIfDifferent(out []byte, path string, value gjson.Result) []byte {
	current := gjson.GetBytes(out, path)
	if current.Exists() && current.Raw == value.Raw {
		return out
	}
	updated, errSet := sjson.SetRawBytes(out, path, []byte(value.Raw))
	if errSet != nil {
		return out
	}
	return updated
}

func antigravityOpenAIToolCallThoughtSignature(tc, m gjson.Result) string {
	for _, path := range []string{
		"extra_content.google.thought_signature",
		"function.extra_content.google.thought_signature",
		"thoughtSignature",
		"thought_signature",
	} {
		if sig := tc.Get(path); sig.Exists() && sig.String() != "" {
			return sigcompat.GeminiReplaySignatureOrBypass(sig.String(), sigcompat.SignatureBlockKindGeminiFunctionCall)
		}
	}
	for _, path := range []string{
		"extra_content.google.thought_signature",
		"thoughtSignature",
		"thought_signature",
	} {
		if sig := m.Get(path); sig.Exists() && sig.String() != "" {
			return sigcompat.GeminiReplaySignatureOrBypass(sig.String(), sigcompat.SignatureBlockKindGeminiFunctionCall)
		}
	}
	return antigravityFunctionThoughtSignature
}

func antigravityOpenAIReasoningContentThoughtSignature(m gjson.Result) string {
	for _, path := range []string{
		"extra_content.google.thought_signature",
		"thoughtSignature",
		"thought_signature",
	} {
		if sig := m.Get(path); sig.Exists() && sig.String() != "" {
			return sigcompat.GeminiReplaySignatureOrBypass(sig.String(), sigcompat.SignatureBlockKindGeminiModelPart)
		}
	}
	return antigravityFunctionThoughtSignature
}

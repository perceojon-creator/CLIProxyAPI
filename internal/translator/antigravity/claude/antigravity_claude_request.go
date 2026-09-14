// Package claude provides request translation functionality for Claude Code API compatibility.
// This package handles the conversion of Claude Code API requests into Antigravity-compatible
// JSON format, transforming message contents, system instructions, and tool declarations
// into the format expected by Antigravity API clients. It performs JSON data transformation
// to ensure compatibility between Claude Code API format and Antigravity API's expected format.
package claude

import (
	"context"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/cache"
	sigcompat "github.com/router-for-me/CLIProxyAPI/v7/internal/signature"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	translatorcommon "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/common"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func resolveThinkingSignature(modelName, thinkingText, rawSignature string) string {
	signature, errSignature := resolveThinkingSignatureRequired(context.Background(), modelName, thinkingText, rawSignature)
	if errSignature != nil {
		return ""
	}
	return signature
}

func resolveThinkingSignatureRequired(ctx context.Context, modelName, thinkingText, rawSignature string) (string, error) {
	targetProvider := sigcompat.SignatureProviderFromModelName(modelName)
	if targetProvider == sigcompat.SignatureProviderGemini {
		innerSignature, _, targetKind, marked, okCarrier := decodeGeminiClaudeCarrierSignature(rawSignature)
		if !okCarrier {
			return "", nil
		}
		blockKind := sigcompat.SignatureBlockKindGeminiModelPart
		if marked && targetKind == geminiClaudeCarrierFunction {
			blockKind = sigcompat.SignatureBlockKindGeminiFunctionCall
		}
		return resolveProviderCompatibleSignature(targetProvider, innerSignature, blockKind), nil
	}
	if cache.SignatureCacheEnabled() {
		return resolveCacheModeSignatureRequired(ctx, modelName, thinkingText, rawSignature)
	}
	if signature := resolveProviderCompatibleSignature(targetProvider, rawSignature, sigcompat.SignatureBlockKindUnknown); signature != "" {
		return signature, nil
	}
	return resolveBypassModeSignatureForProvider(targetProvider, rawSignature), nil
}

func resolveCacheModeSignature(modelName, thinkingText, rawSignature string) string {
	signature, errSignature := resolveCacheModeSignatureRequired(context.Background(), modelName, thinkingText, rawSignature)
	if errSignature != nil {
		return ""
	}
	return signature
}

func resolveCacheModeSignatureRequired(ctx context.Context, modelName, thinkingText, rawSignature string) (string, error) {
	targetProvider := sigcompat.SignatureProviderFromModelName(modelName)

	// 1. Check client-carried provider-native (or legacy prefixed) signature first.
	// If the client provided a signature that is incompatible or invalid, do not
	// fall back to recovery cache.
	if rawSignature != "" {
		if signature := resolveProviderCompatibleSignature(targetProvider, rawSignature, sigcompat.SignatureBlockKindUnknown); signature != "" {
			return signature, nil
		}
		return "", nil
	}

	// 2. Recovery cache only when client omitted signature (rawSignature == "").
	if thinkingText != "" {
		cachedSig, errCachedSig := cache.GetCachedSignatureRequired(ctx, modelName, thinkingText)
		if errCachedSig != nil {
			return "", errCachedSig
		}
		if cachedSig != "" {
			if targetProvider == sigcompat.SignatureProviderClaude {
				signature, ok := sigcompat.CompatibleAntigravityClaudeThinkingSignature(cachedSig)
				if !ok {
					return "", nil
				}
				return signature, nil
			}
			return cachedSig, nil
		}
	}

	return "", nil
}

func RequireCachedThinkingSignatures(ctx context.Context, modelName string, rawJSON []byte) error {
	if !cache.SignatureCacheEnabled() {
		return nil
	}
	if sigcompat.SignatureProviderFromModelName(modelName) == sigcompat.SignatureProviderGemini {
		return nil
	}
	messagesResult := gjson.GetBytes(rawJSON, "messages")
	if !messagesResult.IsArray() {
		return nil
	}
	for _, messageResult := range messagesResult.Array() {
		contentsResult := messageResult.Get("content")
		if !contentsResult.IsArray() {
			continue
		}
		for _, contentResult := range contentsResult.Array() {
			if contentResult.Get("type").String() != "thinking" {
				continue
			}
			thinkingText := thinking.GetThinkingText(contentResult)
			if thinkingText == "" {
				continue
			}
			if _, errSignature := cache.GetCachedSignatureRequired(ctx, modelName, thinkingText); errSignature != nil {
				return errSignature
			}
		}
	}
	return nil
}

func resolveBypassModeSignature(rawSignature string) string {
	return resolveBypassModeSignatureForProvider(sigcompat.SignatureProviderClaude, rawSignature)
}

func resolveBypassModeSignatureForProvider(targetProvider sigcompat.SignatureProvider, rawSignature string) string {
	if rawSignature == "" {
		return ""
	}
	if targetProvider != sigcompat.SignatureProviderClaude && targetProvider != sigcompat.SignatureProviderUnknown {
		return ""
	}
	if targetProvider == sigcompat.SignatureProviderClaude {
		signature, ok := sigcompat.CompatibleAntigravityClaudeThinkingSignature(rawSignature)
		if !ok {
			return ""
		}
		return signature
	}
	normalized, err := normalizeClaudeBypassSignature(rawSignature)
	if err != nil {
		return ""
	}
	return normalized
}

func hasResolvedThinkingSignature(modelName, signature string) bool {
	targetProvider := sigcompat.SignatureProviderFromModelName(modelName)
	if targetProvider == sigcompat.SignatureProviderClaude {
		_, ok := sigcompat.CompatibleAntigravityClaudeThinkingSignature(signature)
		return ok
	}
	if _, ok := sigcompat.CompatibleSignatureForProvider(targetProvider, signature); ok {
		return true
	}
	if cache.SignatureCacheEnabled() {
		return cache.HasValidSignature(modelName, signature)
	}
	return signature != ""
}

func resolveProviderCompatibleSignature(targetProvider sigcompat.SignatureProvider, rawSignature string, blockKind sigcompat.SignatureBlockKind) string {
	if rawSignature == "" {
		return ""
	}
	if targetProvider == sigcompat.SignatureProviderClaude {
		signature, ok := sigcompat.CompatibleAntigravityClaudeThinkingSignature(rawSignature)
		if !ok {
			return ""
		}
		return signature
	}
	signature, ok := sigcompat.CompatibleSignatureForProviderBlock(targetProvider, rawSignature, blockKind)
	if !ok {
		return ""
	}
	return signature
}

func resolveToolUseThoughtSignature(modelName string, contentResult gjson.Result, allowSyntheticFallback bool) string {
	targetProvider := sigcompat.SignatureProviderFromModelName(modelName)
	if targetProvider == sigcompat.SignatureProviderGemini {
		for _, path := range []string{
			"signature",
			"thought_signature",
			"extra_content.google.thought_signature",
		} {
			if signatureResult := contentResult.Get(path); signatureResult.Exists() {
				if signature := resolveProviderCompatibleSignature(targetProvider, signatureResult.String(), sigcompat.SignatureBlockKindGeminiFunctionCall); signature != "" {
					return signature
				}
			}
		}
		if allowSyntheticFallback {
			return sigcompat.GeminiSkipThoughtSignatureValidator
		}
		return ""
	}

	for _, path := range []string{
		"signature",
		"thought_signature",
		"extra_content.google.thought_signature",
	} {
		if signatureResult := contentResult.Get(path); signatureResult.Exists() {
			if signature := resolveProviderCompatibleSignature(targetProvider, signatureResult.String(), sigcompat.SignatureBlockKindUnknown); signature != "" {
				return signature
			}
		}
	}
	if targetProvider == sigcompat.SignatureProviderClaude {
		return ""
	}
	return sigcompat.GeminiSkipThoughtSignatureValidator
}

func firstToolUseSignatureField(contentResult gjson.Result) (string, string, bool) {
	for _, path := range []string{
		"signature",
		"thought_signature",
		"extra_content.google.thought_signature",
	} {
		signatureResult := contentResult.Get(path)
		if signatureResult.Exists() {
			return path, signatureResult.String(), true
		}
	}
	return "", "", false
}

func logDroppedAntigravityThinkingSignature(modelName string, messageIndex, contentIndex int, thinkingText string, signatureResult gjson.Result) {
	rawSignature := signatureResult.String()
	fields := log.Fields{
		"component":        "signature_sanitizer",
		"translator":       "antigravity_claude",
		"target_provider":  string(sigcompat.SignatureProviderFromModelName(modelName)),
		"action":           "drop_thinking_block",
		"reason":           "missing_or_incompatible_signature",
		"model":            modelName,
		"message_index":    messageIndex,
		"content_index":    contentIndex,
		"thinking_length":  len(thinkingText),
		"has_signature":    signatureResult.Exists(),
		"signature_length": len(strings.TrimSpace(rawSignature)),
	}
	if signatureResult.Exists() {
		fields["detected_provider"] = string(sigcompat.DetectSignatureProviderForBlock(rawSignature, sigcompat.SignatureBlockKindClaudeThinking))
	}
	log.WithFields(fields).Debug("antigravity claude translator: dropped thinking block with incompatible signature")
}

func logDroppedAntigravityEmptyThinking(modelName string, messageIndex, contentIndex int) {
	log.WithFields(log.Fields{
		"component":       "signature_sanitizer",
		"translator":      "antigravity_claude",
		"target_provider": string(sigcompat.SignatureProviderFromModelName(modelName)),
		"action":          "drop_thinking_block",
		"reason":          "empty_thinking_text",
		"model":           modelName,
		"message_index":   messageIndex,
		"content_index":   contentIndex,
	}).Debug("antigravity claude translator: dropped empty thinking block")
}

func logDroppedAntigravityToolUseSignature(modelName string, messageIndex, contentIndex int, contentResult gjson.Result) {
	path, rawSignature, ok := firstToolUseSignatureField(contentResult)
	if !ok {
		return
	}
	log.WithFields(log.Fields{
		"component":         "signature_sanitizer",
		"translator":        "antigravity_claude",
		"target_provider":   string(sigcompat.SignatureProviderFromModelName(modelName)),
		"action":            "drop_tool_use_signature",
		"reason":            "missing_or_incompatible_signature",
		"model":             modelName,
		"message_index":     messageIndex,
		"content_index":     contentIndex,
		"signature_path":    path,
		"signature_length":  len(strings.TrimSpace(rawSignature)),
		"detected_provider": string(sigcompat.DetectSignatureProviderForBlock(rawSignature, sigcompat.SignatureBlockKindUnknown)),
	}).Debug("antigravity claude translator: dropped tool_use signature field")
}

// ConvertClaudeRequestToAntigravity parses and transforms a Claude Code API request into Antigravity API format.
// It extracts the model name, system instruction, message contents, and tool declarations
// from the raw JSON request and returns them in the format expected by the Antigravity API.
// The function performs the following transformations:
// 1. Extracts the model information from the request
// 2. Restructures the JSON to match Antigravity API format
// 3. Converts system instructions to the expected format
// 4. Maps message contents with proper role transformations
// 5. Handles tool declarations and tool choices
// 6. Maps generation configuration parameters
//
// Parameters:
//   - modelName: The name of the model to use for the request
//   - rawJSON: The raw JSON request data from the Claude Code API
//   - stream: A boolean indicating if the request is for a streaming response (unused in current implementation)
//
// Returns:
//   - []byte: The transformed request data in Antigravity API format
func ConvertClaudeRequestToAntigravity(modelName string, inputRawJSON []byte, _ bool) []byte {
	rawJSON := inputRawJSON
	if shouldBuildAntigravityWebSearchRequest(modelName, rawJSON) {
		return buildAntigravityWebSearchRequest(modelName, rawJSON)
	}

	functionNameMap := util.SanitizedFunctionNameMap(rawJSON)

	// Phase 1: Extractor de Instrucciones de Sistema (System Prompt)
	systemParts := extractAntigravityClaudeSystemParts(rawJSON)

	// Phase 2: Normalizador de Herramientas (Tool / Function Calling Declarations)
	toolsJSON, toolDeclCount := extractAntigravityClaudeTools(rawJSON, functionNameMap)

	// Phase 3: Procesador de Modalidades, Pensamiento y Contenidos (Images, Thinking, Messages)
	contentItems, enableThoughtTranslate := extractAntigravityClaudeContents(modelName, rawJSON, functionNameMap)

	// Phase 4: Ensamblador del Payload Destino (GenerationConfig, ToolChoice, Assembly)
	return assembleAntigravityClaudePayload(
		modelName,
		rawJSON,
		systemParts,
		contentItems,
		toolsJSON,
		toolDeclCount,
		functionNameMap,
		enableThoughtTranslate,
	)
}
func antigravityClaudeContent(role string, parts [][]byte) []byte {
	content := []byte(`{"role":"","parts":[]}`)
	content, _ = sjson.SetBytes(content, "role", role)
	content, _ = sjson.SetRawBytes(content, "parts", translatorcommon.JoinRawArray(parts))
	return content
}

package registry

import "testing"

// kiroExpectedModels is the full Kiro catalogue exposed through the model registry.
var kiroExpectedModels = []string{
	"auto",
	"claude-opus-5",
	"claude-sonnet-5",
	"claude-opus-4.8",
	"claude-opus-4.7",
	"claude-opus-4.6",
	"claude-opus-4.5",
	"claude-sonnet-4.6",
	"claude-sonnet-4.5",
	"claude-sonnet-4",
	"claude-haiku-4.5",
	"gpt-5.6-sol",
	"gpt-5.6-terra",
	"gpt-5.6-luna",
	"glm-5",
	"deepseek-3.2",
	"minimax-m2.5",
	"minimax-m2.1",
	"qwen3-coder-next",
}

func TestGetKiroModelsExposesFullCatalogue(t *testing.T) {
	models := GetKiroModels()
	if len(models) != len(kiroExpectedModels) {
		t.Fatalf("Kiro models = %d, want %d", len(models), len(kiroExpectedModels))
	}

	byID := make(map[string]*ModelInfo, len(models))
	for _, model := range models {
		if model == nil {
			t.Fatal("Kiro models contain a nil entry")
		}
		byID[model.ID] = model
	}

	for _, id := range kiroExpectedModels {
		model, ok := byID[id]
		if !ok {
			t.Fatalf("Kiro models missing %q", id)
		}
		if model.Type != "kiro" {
			t.Fatalf("%s type = %q, want kiro", id, model.Type)
		}
		if model.Object != "model" {
			t.Fatalf("%s object = %q, want model", id, model.Object)
		}
		if model.DisplayName == "" {
			t.Fatalf("%s has no display name", id)
		}
		if model.ContextLength != 200000 {
			t.Fatalf("%s context length = %d, want 200000", id, model.ContextLength)
		}
		if model.MaxCompletionTokens != 64000 {
			t.Fatalf("%s max completion tokens = %d, want 64000", id, model.MaxCompletionTokens)
		}
		if model.Created <= 0 {
			t.Fatalf("%s created = %d, want a positive timestamp", id, model.Created)
		}
		if len(model.SupportedInputModalities) == 0 || model.SupportedInputModalities[0] != "text" {
			t.Fatalf("%s input modalities = %v, want text first", id, model.SupportedInputModalities)
		}
		if len(model.SupportedOutputModalities) != 1 || model.SupportedOutputModalities[0] != "text" {
			t.Fatalf("%s output modalities = %v, want [text]", id, model.SupportedOutputModalities)
		}
		// The Kiro runtime has no thinking budget knob, so no model may advertise one.
		if model.Thinking != nil {
			t.Fatalf("%s advertises thinking support, but Kiro exposes no thinking control", id)
		}
	}
}

func TestGetKiroModelsImageCapableSubset(t *testing.T) {
	textOnly := map[string]bool{
		"glm-5":            true,
		"deepseek-3.2":     true,
		"minimax-m2.5":     true,
		"minimax-m2.1":     true,
		"qwen3-coder-next": true,
	}
	for _, model := range GetKiroModels() {
		hasImage := false
		for _, modality := range model.SupportedInputModalities {
			if modality == "image" {
				hasImage = true
				break
			}
		}
		if textOnly[model.ID] && hasImage {
			t.Fatalf("%s should be text-only", model.ID)
		}
		if !textOnly[model.ID] && !hasImage {
			t.Fatalf("%s should accept image input", model.ID)
		}
	}
}

func TestGetStaticModelDefinitionsByChannelSupportsKiro(t *testing.T) {
	if models := GetStaticModelDefinitionsByChannel("kiro"); len(models) != len(kiroExpectedModels) {
		t.Fatalf("kiro channel models = %d, want %d", len(models), len(kiroExpectedModels))
	}
	if models := GetStaticModelDefinitionsByChannel("KIRO"); len(models) != len(kiroExpectedModels) {
		t.Fatal("channel lookup must be case-insensitive")
	}
}

func TestGetKiroModelsReturnsClones(t *testing.T) {
	first := GetKiroModels()
	if len(first) == 0 {
		t.Fatal("GetKiroModels() returned nothing")
	}
	original := first[0].DisplayName
	first[0].DisplayName = "mutated"

	if got := GetKiroModels()[0].DisplayName; got != original {
		t.Fatalf("display name = %q, want %q — definitions must be cloned", got, original)
	}
}

func TestLookupStaticModelInfoFindsKiroOnlyModels(t *testing.T) {
	info := LookupStaticModelInfo("qwen3-coder-next")
	if info == nil {
		t.Fatal("LookupStaticModelInfo(qwen3-coder-next) = nil, want the Kiro definition")
	}
	if info.Type != "kiro" {
		t.Fatalf("type = %q, want kiro", info.Type)
	}
}

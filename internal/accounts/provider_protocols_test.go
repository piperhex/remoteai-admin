package accounts

import (
	"encoding/json"
	"reflect"
	"testing"
)

func mixedProtocolProviderFixture() object {
	return object{
		"id": "mixed-protocols", "name": "Mixed protocols", "apiKey": "fixture-only",
		"baseUrl": "https://provider.example", "model": "responses",
		"models":    []interface{}{"responses", "chat", "claude", "gemini", "default"},
		"apiFormat": "openaiResponses",
		"modelApiFormats": object{
			"responses": "openaiResponses", "chat": "openaiChat",
			"claude": "anthropicMessages", "gemini": "geminiGenerateContent",
		},
		"lastModifiedAt": testOld,
	}
}

func TestProviderMixedProtocolsSurviveUploadAndDownload(t *testing.T) {
	incoming := mixedProtocolProviderFixture()
	stored, err := mergeProvider(nil, "owner", incoming)
	if err != nil || stored == nil {
		t.Fatalf("provider upload failed: %v", err)
	}
	if !reflect.DeepEqual(stored.ModelAPIFormats, incoming["modelApiFormats"]) {
		t.Fatalf("upload changed model protocols: %#v", stored.ModelAPIFormats)
	}
	// Exercise the download DTO and JSON transport before a second desktop uploads it.
	encoded, err := json.Marshal(providerDTO(*stored))
	if err != nil {
		t.Fatal(err)
	}
	downloaded := object{}
	if err = json.Unmarshal(encoded, &downloaded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(downloaded["modelApiFormats"], incoming["modelApiFormats"]) ||
		downloaded["apiFormat"] != incoming["apiFormat"] {
		t.Fatalf("download changed model or account protocols: %#v", downloaded)
	}
	downloaded["name"] = "Renamed on another desktop"
	downloaded["fieldModifiedAt"].(map[string]interface{})["name"] = testNew
	updated, err := mergeProvider(stored, "owner", downloaded)
	if err != nil || updated == nil {
		t.Fatalf("second desktop upload failed: %v", err)
	}
	if updated.Name != downloaded["name"] ||
		!reflect.DeepEqual(providerDTO(*updated)["modelApiFormats"], incoming["modelApiFormats"]) {
		t.Fatal("independent desktop update changed model protocols")
	}
}

func TestProviderModelProtocolUpdatePreservesAccountDefault(t *testing.T) {
	incoming := mixedProtocolProviderFixture()
	incoming["modelApiFormats"].(object)["gemini"] = "openaiChat"
	stored, err := mergeProvider(nil, "owner", incoming)
	if err != nil || stored == nil {
		t.Fatalf("initial upload failed: %v", err)
	}
	incoming["modelApiFormats"].(object)["gemini"] = "geminiGenerateContent"
	incoming["fieldModifiedAt"] = object{"modelApiFormats": testNew}
	updated, err := mergeProvider(stored, "owner", incoming)
	if err != nil || updated == nil {
		t.Fatalf("protocol update failed: %v", err)
	}
	if updated.APIFormat != stored.APIFormat ||
		!reflect.DeepEqual(providerDTO(*updated)["modelApiFormats"], incoming["modelApiFormats"]) {
		t.Fatal("protocol update was lost or changed the account default")
	}
	incoming["modelApiFormats"].(object)["gemini"] = "openaiChat"
	incoming["name"] = "Concurrent rename"
	incoming["fieldModifiedAt"] = object{"name": testNew, "modelApiFormats": testOld}
	merged, err := mergeProvider(updated, "owner", incoming)
	if err != nil || merged == nil || merged.ModelAPIFormats["gemini"] != "geminiGenerateContent" {
		t.Fatalf("stale desktop overwrote the Gemini override: %#v, %v", merged, err)
	}
}

func TestProviderModelProtocolsFilterUnknownFormatsAndRemovedModels(t *testing.T) {
	configured := object{
		"gemini": "geminiGenerateContent", "unknown": "unsupported", "removed": "geminiGenerateContent",
		"empty": "", "malformed": object{"format": "geminiGenerateContent"},
	}
	actual := normalizeModels("modelApiFormats", configured, []string{"gemini", "unknown", "empty", "malformed"})
	if !reflect.DeepEqual(actual, object{"gemini": "geminiGenerateContent"}) {
		t.Fatalf("unexpected model protocols: %#v", actual)
	}
}

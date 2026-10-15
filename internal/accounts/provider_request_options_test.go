package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
)

func providerOptionsFixture() object {
	return object{
		"id": "provider-options", "name": "Original", "apiKey": "fixture-only",
		"baseUrl": "https://provider.example/v1", "model": "model", "models": []interface{}{"model"},
		"apiFormat": "anthropicMessages", "modelApiFormats": object{"model": "anthropicMessages"},
		"lastModifiedAt": testOld,
		"requestOptions": object{"headers": object{"X-Region": "east"}, "body": object{"temperature": 0.2}},
	}
}

func TestProviderRequestOptionsCreateUpdateAndClear(t *testing.T) {
	incoming := providerOptionsFixture()
	stored, err := mergeProvider(nil, "owner", incoming)
	if err != nil || stored == nil || !reflect.DeepEqual(stored.RequestOptions, incoming["requestOptions"]) {
		t.Fatalf("initial options were not stored: %#v, %v", stored, err)
	}
	if stored.ModelAPIFormats["model"] != "anthropicMessages" {
		t.Fatal("native protocol was removed during synchronization")
	}
	for _, options := range []object{{"useFullUrl": true, "headers": object{"X-Region": "west"}}, {}} {
		incoming["requestOptions"] = options
		incoming["fieldModifiedAt"] = object{"requestOptions": testNew}
		updated, err := mergeProvider(stored, "owner", incoming)
		if err != nil || updated == nil || !reflect.DeepEqual(updated.RequestOptions, options) {
			t.Fatalf("options were not replaced: %#v, %v", updated, err)
		}
		if !reflect.DeepEqual(providerDTO(*updated)["requestOptions"], options) {
			t.Fatal("pull response lost the saved options")
		}
	}
}

func TestProviderRequestOptionsMissingAndNullNeverEraseStoredSettings(t *testing.T) {
	stored, err := mergeProvider(nil, "owner", providerOptionsFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, omit := range []bool{false, true} {
		incoming := providerOptionsFixture()
		incoming["requestOptions"] = nil
		if omit {
			delete(incoming, "requestOptions")
		}
		incoming["lastModifiedAt"] = testNew
		incoming["fieldModifiedAt"] = object{"name": testNew}
		incoming["name"] = "Renamed"
		updated, err := mergeProvider(stored, "owner", incoming)
		if err != nil || updated == nil || updated.Name != "Renamed" {
			t.Fatalf("legacy field update failed: %#v, %v", updated, err)
		}
		if !reflect.DeepEqual(updated.RequestOptions, stored.RequestOptions) ||
			updated.FieldModifiedAt["requestOptions"] != stored.FieldModifiedAt["requestOptions"] {
			t.Fatal("missing advanced settings changed their stored value or timestamp")
		}
		delete(incoming, "fieldModifiedAt")
		if updated, err = mergeProvider(stored, "owner", incoming); err != nil || updated != nil {
			t.Fatal("unversioned legacy payload bypassed conflict protection")
		}
	}
}

func TestProviderRequestOptionsVersionsAreIndependent(t *testing.T) {
	stored, err := mergeProvider(nil, "owner", providerOptionsFixture())
	if err != nil {
		t.Fatal(err)
	}
	options := object{"body": object{"thinking": object{"type": "enabled"}}}
	incoming := providerOptionsFixture()
	incoming["requestOptions"] = options
	incoming["name"] = "Stale name"
	incoming["fieldModifiedAt"] = object{"requestOptions": testNew}
	updated, err := mergeProvider(stored, "owner", incoming)
	if err != nil || updated == nil || updated.Name != stored.Name {
		t.Fatalf("options update overwrote another field: %#v, %v", updated, err)
	}
	incoming["requestOptions"] = object{}
	incoming["name"] = "New name"
	incoming["fieldModifiedAt"] = object{"name": testNew, "requestOptions": testOld}
	merged, err := mergeProvider(updated, "owner", incoming)
	if err != nil || merged == nil || merged.Name != "New name" || !reflect.DeepEqual(merged.RequestOptions, options) {
		t.Fatalf("stale options overwrote a newer field version: %#v, %v", merged, err)
	}
}

func TestProviderRequestOptionsRejectMalformedInput(t *testing.T) {
	cases := map[string]interface{}{
		"scalar": "value", "array": []interface{}{}, "unknown": object{"extra": true},
		"full URL type": object{"useFullUrl": "true"}, "full URL null": object{"useFullUrl": nil},
		"headers array": object{"headers": []interface{}{}}, "headers null": object{"headers": nil},
		"header value":     object{"headers": object{"X-Region": true}},
		"header name":      object{"headers": object{"Bad Header": "value"}},
		"header injection": object{"headers": object{"X-Region": "east\r\nHost: attacker"}},
		"duplicate header": object{"headers": object{"X-Region": "east", "x-region": "west"}},
		"large header":     object{"headers": object{"X-Region": strings.Repeat("a", maxProviderHeaderBytes+1)}},
		"body array":       object{"body": []interface{}{}}, "body null": object{"body": nil},
		"oversize": object{"body": object{"extra": strings.Repeat("a", maxProviderOptionsBytes)}},
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			incoming := providerOptionsFixture()
			incoming["requestOptions"] = value
			row, err := mergeProvider(nil, "owner", incoming)
			var response *platform.HTTPError
			if row != nil || !errors.As(err, &response) || response.Status != http.StatusBadRequest {
				t.Fatalf("invalid options did not return a client error: %#v, %v", row, err)
			}
		})
	}
}

func TestProviderRequestOptionsProtectCredentialsAndProtocol(t *testing.T) {
	headers := []string{
		"Authorization", "Proxy-Authorization", "X-API-Key", "api-key", "X-Goog-Api-Key",
		"Cookie", "Set-Cookie", "Host", "Connection", "Content-Length", "Content-Type",
		"Transfer-Encoding", "Upgrade", "TE", "Trailer", "Keep-Alive", "Proxy-Connection",
		"Forwarded", "X-Forwarded-For", "X-Codex-Session", "ChatGPT-Account-ID",
	}
	for _, name := range headers {
		if err := validateProviderRequestOptions(object{"headers": object{name: "value"}}); err == nil {
			t.Errorf("protected header accepted: %s", name)
		}
	}
	fields := []string{
		"model", "stream", "stream_options", "input", "messages", "tools", "tool_choice", "system",
		"instructions", "previous_response_id", "conversation", "include", "api_key", "apiKey", "authorization",
		"contents", "systemInstruction", "system_instruction", "cachedContent", "cached_content", "toolConfig", "tool_config",
	}
	for _, name := range fields {
		if err := validateProviderRequestOptions(object{"body": object{name: "value"}}); err == nil {
			t.Errorf("protected parameter accepted: %s", name)
		}
	}
}

func TestProviderRequestOptionsLimitsAndOptionalValues(t *testing.T) {
	for _, options := range []interface{}{nil, object{}, object{"headers": object{}, "body": object{}}} {
		if err := validateProviderRequestOptions(options); err != nil {
			t.Fatal(err)
		}
	}
	headers := object{}
	for i := 0; i < maxProviderHeaders; i++ {
		headers[fmt.Sprintf("X-Test-%d", i)] = "ok"
	}
	if err := validateProviderRequestOptions(object{"headers": headers}); err != nil {
		t.Fatal(err)
	}
	headers["X-Excess"] = "no"
	if err := validateProviderRequestOptions(object{"headers": headers}); err == nil {
		t.Fatal("too many headers accepted")
	}
	var nested interface{} = "leaf"
	for i := 1; i < maxProviderBodyDepth; i++ {
		nested = object{"child": nested}
	}
	if err := validateProviderRequestOptions(object{"body": object{"extra": nested}}); err != nil {
		t.Fatal("maximum supported depth rejected", err)
	}
	nested = []interface{}{nested}
	if err := validateProviderRequestOptions(object{"body": object{"extra": nested}}); err == nil {
		t.Fatal("excessive nested arrays accepted")
	}
}

func TestProviderRequestOptionsSizeMatchesNormalizedClientJSON(t *testing.T) {
	base := object{"useFullUrl": false, "headers": object{}, "body": object{"extra": ""}}
	encoded, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	limit := maxProviderOptionsBytes - len(encoded)
	for _, prefix := range []string{"", "\u2028\u2029", `\u2028\u2029`} {
		prefixJSON, err := json.Marshal(prefix)
		if err != nil {
			t.Fatal(err)
		}
		prefixSize := len(prefixJSON) - 2
		if prefix == "\u2028\u2029" {
			prefixSize = len(prefix)
		}
		value := prefix + strings.Repeat("a", limit-prefixSize)
		options := object{"body": object{"extra": value}}
		if err := validateProviderRequestOptions(options); err != nil {
			t.Fatal("exactly 16 KiB of normalized JSON rejected", err)
		}
		options["body"] = object{"extra": value + "b"}
		if err := validateProviderRequestOptions(options); err == nil {
			t.Fatal("normalized JSON exceeding 16 KiB accepted")
		}
	}
}

func TestProviderRequestOptionsRejectPrototypeKeysAtEveryDepth(t *testing.T) {
	for _, key := range []string{"__proto__", "constructor", "prototype"} {
		for _, body := range []object{
			{key: true}, {"extra": object{key: true}}, {"extra": []interface{}{object{key: true}}},
		} {
			if err := validateProviderRequestOptions(object{"body": body}); err == nil {
				t.Fatalf("unsafe nested object key accepted: %s", key)
			}
		}
		if err := validateProviderRequestOptions(object{"headers": object{strings.ToUpper(key): "value"}}); err == nil {
			t.Fatalf("unsafe header key accepted: %s", key)
		}
	}
}

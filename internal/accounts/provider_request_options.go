package accounts

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/codex-switch/admin-go/internal/platform"
	"golang.org/x/net/http/httpguts"
)

const (
	maxProviderOptionsBytes = 16 * 1024
	maxProviderHeaders      = 32
	maxProviderHeaderBytes  = 4096
	maxProviderBodyDepth    = 8
)

func validateProviderRequestOptions(value interface{}) error {
	// Omitted and null values come from clients that predate these optional settings.
	if value == nil {
		return nil
	}
	options, ok := value.(object)
	if !ok || options == nil {
		return providerOptionsError("Advanced settings must be an object")
	}
	for key := range options {
		if key != "useFullUrl" && key != "headers" && key != "body" {
			return providerOptionsError("Advanced settings contain an unknown option")
		}
	}
	if fullURL, exists := options["useFullUrl"]; exists {
		if _, ok := fullURL.(bool); !ok {
			return providerOptionsError("The full URL setting must be true or false")
		}
	}
	if headers, exists := options["headers"]; exists {
		if err := validateProviderHeaders(headers); err != nil {
			return err
		}
	}
	if body, exists := options["body"]; exists {
		if err := validateProviderBody(body); err != nil {
			return err
		}
	}
	return validateProviderOptionsSize(options)
}

func validateProviderOptionsSize(options object) error {
	normalized := object{"useFullUrl": false, "headers": object{}, "body": object{}}
	for key, value := range options {
		normalized[key] = value
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(normalized); err != nil ||
		providerOptionsJSONSize(encoded.Bytes()) > maxProviderOptionsBytes {
		return providerOptionsError("Advanced settings are too large or contain invalid values")
	}
	return nil
}

func providerOptionsJSONSize(encoded []byte) int {
	// Rust and browser JSON keep these UTF-8 characters literal; Go always escapes them.
	size := len(encoded) - 1 // Encoder adds one trailing newline.
	for index := 0; index+1 < len(encoded); index++ {
		if encoded[index] != '\\' {
			continue
		}
		if bytes.HasPrefix(encoded[index:], []byte(`\u2028`)) || bytes.HasPrefix(encoded[index:], []byte(`\u2029`)) {
			size -= 3
		}
		index++ // Skip an escape pair so literal backslashes are not mistaken for Unicode.
	}
	return size
}

func validateProviderHeaders(value interface{}) error {
	headers, ok := value.(object)
	if !ok || headers == nil {
		return providerOptionsError("Custom headers must be an object with text values")
	}
	if len(headers) > maxProviderHeaders {
		return providerOptionsError("There are too many custom headers")
	}
	names := make(map[string]bool, len(headers))
	for name, raw := range headers {
		text, ok := raw.(string)
		lower := strings.ToLower(name)
		if !ok || names[lower] || protectedProviderHeader(lower) ||
			!httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(text) ||
			len(text) > maxProviderHeaderBytes {
			return providerOptionsError("A custom header is invalid or reserved")
		}
		names[lower] = true
	}
	return nil
}

func protectedProviderHeader(name string) bool {
	if prototypeProviderKey(name) || strings.HasPrefix(name, "x-forwarded-") || strings.HasPrefix(name, "x-codex-") {
		return true
	}
	switch name {
	case "authorization", "proxy-authorization", "x-api-key", "api-key", "x-goog-api-key",
		"cookie", "set-cookie", "host", "connection", "content-length", "content-type",
		"transfer-encoding", "upgrade", "te", "trailer", "keep-alive", "proxy-connection",
		"forwarded", "chatgpt-account-id":
		return true
	}
	return false
}

func validateProviderBody(value interface{}) error {
	body, ok := value.(object)
	if !ok || body == nil {
		return providerOptionsError("Additional parameters must be an object")
	}
	for key, value := range body {
		if protectedProviderParameter(key) {
			return providerOptionsError("Additional parameters cannot replace conversation or authentication fields")
		}
		if providerBodyTooDeep(value, 1) {
			return providerOptionsError("Additional parameters are nested too deeply")
		}
		if prototypeProviderKey(key) || providerBodyHasPrototypeKey(value) {
			return providerOptionsError("Additional parameters contain a reserved object key")
		}
	}
	return nil
}

func protectedProviderParameter(name string) bool {
	switch name {
	case "model", "stream", "stream_options", "input", "messages", "tools", "tool_choice",
		"system", "instructions", "previous_response_id", "conversation", "include",
		"api_key", "apiKey", "authorization", "contents", "systemInstruction", "system_instruction",
		"cachedContent", "cached_content", "toolConfig", "tool_config":
		return true
	}
	return false
}

func providerBodyTooDeep(value interface{}, depth int) bool {
	if depth > maxProviderBodyDepth {
		return true
	}
	switch children := value.(type) {
	case object:
		for _, child := range children {
			if providerBodyTooDeep(child, depth+1) {
				return true
			}
		}
	case []interface{}:
		for _, child := range children {
			if providerBodyTooDeep(child, depth+1) {
				return true
			}
		}
	}
	return false
}

func prototypeProviderKey(name string) bool {
	return name == "__proto__" || name == "constructor" || name == "prototype"
}

func providerBodyHasPrototypeKey(value interface{}) bool {
	switch children := value.(type) {
	case object:
		for name, child := range children {
			if prototypeProviderKey(name) || providerBodyHasPrototypeKey(child) {
				return true
			}
		}
	case []interface{}:
		for _, child := range children {
			if providerBodyHasPrototypeKey(child) {
				return true
			}
		}
	}
	return false
}

func providerOptionsError(message string) error {
	return platform.NewError(http.StatusBadRequest, message)
}

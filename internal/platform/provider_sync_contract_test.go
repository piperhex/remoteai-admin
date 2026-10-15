package platform

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func providerSyncContractRouter(t *testing.T) *gin.Engine {
	t.Helper()
	contract, err := LoadContract()
	if err != nil {
		t.Fatal(err)
	}
	deps := &Dependencies{Authenticate: func(*gin.Context) (*Principal, error) {
		return &Principal{ID: "owner", Permissions: []string{"self.providers.write"}}, nil
	}}
	router := gin.New()
	router.Use(RequestBodyParser(), contract.Middleware(deps))
	for _, path := range []string{"/sync/providers", "/sync/providers/:id"} {
		router.PUT(path, func(c *gin.Context) { c.JSON(http.StatusOK, Body(c)) })
	}
	return router
}

func providerSyncContractPayload() JSON {
	return JSON{
		"id": "provider", "name": "Provider", "baseUrl": "https://provider.example/v1",
		"apiKey": "fixture-only", "model": "model", "apiFormat": "openaiResponses",
		"fieldModifiedAt": JSON{"requestOptions": "2026-10-15T10:00:00Z"},
	}
}

func sendProviderSyncRequest(t *testing.T, router *gin.Engine, path string, payload JSON) *httptest.ResponseRecorder {
	t.Helper()
	if path == "/sync/providers" {
		payload = JSON{"providers": []interface{}{payload}}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestProviderSyncContractAcceptsRequestOptions(t *testing.T) {
	router := providerSyncContractRouter(t)
	cases := map[string]JSON{
		"unconfigured": {},
		"null":         {"requestOptions": nil},
		"clear":        {"requestOptions": JSON{}},
		"configured": {"requestOptions": JSON{
			"useFullUrl": true, "headers": JSON{"X-Region": "west"}, "body": JSON{"temperature": 0.2},
		}},
	}
	for name, fields := range cases {
		for _, path := range []string{"/sync/providers", "/sync/providers/provider"} {
			t.Run(name+path, func(t *testing.T) {
				payload := providerSyncContractPayload()
				for key, value := range fields {
					payload[key] = value
				}
				response := sendProviderSyncRequest(t, router, path, payload)
				if response.Code != http.StatusOK {
					t.Fatalf("upload rejected: %d %s", response.Code, response.Body)
				}
				assertProviderSyncOptionsPreserved(t, response, path, payload)
			})
		}
	}
}

func assertProviderSyncOptionsPreserved(t *testing.T, response *httptest.ResponseRecorder, path string, input JSON) {
	t.Helper()
	var output JSON
	if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if path == "/sync/providers" {
		output = output["providers"].([]interface{})[0].(map[string]interface{})
	}
	for _, key := range []string{"requestOptions", "fieldModifiedAt"} {
		got, gotPresent := output[key]
		want, wantPresent := input[key]
		if gotPresent != wantPresent || !reflect.DeepEqual(got, want) {
			t.Errorf("%s changed at the HTTP boundary: got %#v, want %#v", key, output, input)
		}
	}
}

func TestProviderSyncContractRejectsInvalidRequestOptions(t *testing.T) {
	router := providerSyncContractRouter(t)
	cases := []struct {
		name, field, message string
		value                interface{}
	}{
		{"scalar", "requestOptions", "requestOptions must be an object", true},
		{"array", "requestOptions", "requestOptions must be an object", []interface{}{}},
		{"timestamp type", "fieldModifiedAt", "requestOptions must be a string", JSON{"requestOptions": true}},
		{"timestamp length", "fieldModifiedAt", "requestOptions must be shorter than or equal to 40 characters",
			JSON{"requestOptions": strings.Repeat("x", 41)}},
		{"unknown field", "unexpected", "property unexpected should not exist", true},
		{"unknown version", "fieldModifiedAt", "property unexpected should not exist", JSON{"unexpected": "value"}},
	}
	for _, test := range cases {
		for _, path := range []string{"/sync/providers", "/sync/providers/provider"} {
			t.Run(test.name+path, func(t *testing.T) {
				payload := providerSyncContractPayload()
				payload[test.field] = test.value
				response := sendProviderSyncRequest(t, router, path, payload)
				if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), test.message) {
					t.Fatalf("unexpected validation response: %d %s", response.Code, response.Body)
				}
			})
		}
	}
}

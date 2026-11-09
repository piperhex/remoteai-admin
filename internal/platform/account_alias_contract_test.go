package platform

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type accountAliasRoute struct {
	method, path string
}

var accountAliasRoutes = []accountAliasRoute{
	{http.MethodPut, "/sync/accounts"},
	{http.MethodPut, "/sync/accounts/:id"},
	{http.MethodPatch, "/sync/accounts/:id/details"},
}

func accountAliasRouter(t *testing.T, principal *Principal) *gin.Engine {
	t.Helper()
	contract, err := LoadContract()
	if err != nil {
		t.Fatal(err)
	}
	deps := &Dependencies{Authenticate: func(*gin.Context) (*Principal, error) { return principal, nil }}
	router := gin.New()
	router.Use(RequestBodyParser(), contract.Middleware(deps))
	for _, route := range accountAliasRoutes {
		router.Handle(route.method, route.path, func(c *gin.Context) { c.JSON(http.StatusOK, Body(c)) })
	}
	return router
}

func sendAccountAliasRequest(
	t *testing.T, router *gin.Engine, route accountAliasRoute, details JSON,
) *httptest.ResponseRecorder {
	t.Helper()
	payload := JSON{"privateDetails": details}
	if route.method == http.MethodPut {
		payload["id"], payload["email"], payload["plan"] = "account", "fixture@example.test", "plus"
		payload["active"], payload["auth"], payload["usage"] = false, JSON{}, JSON{}
		payload["fieldModifiedAt"] = JSON{"privateDetails": "2026-11-09T00:00:00.000Z"}
	}
	if route.path == "/sync/accounts" {
		payload = JSON{"accounts": []interface{}{payload}}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(route.method, strings.ReplaceAll(route.path, ":id", "account"),
		bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestAccountAliasContractAcceptsAliasesAndLegacyClients(t *testing.T) {
	router := accountAliasRouter(t, &Principal{ID: "owner", Permissions: []string{"self.accounts.write"}})
	cases := map[string]JSON{
		"legacy omitted":  {"phoneNumber": "123"},
		"clear":           {"alias": ""},
		"chinese":         {"alias": "工作号"},
		"unicode limit":   {"alias": strings.Repeat("😀", 16)},
		"trim in service": {"alias": "  " + strings.Repeat("中", 16) + "  "},
	}
	for name, details := range cases {
		for _, route := range accountAliasRoutes {
			t.Run(name+route.path, func(t *testing.T) {
				response := sendAccountAliasRequest(t, router, route, details)
				if response.Code != http.StatusOK {
					t.Fatalf("account details rejected: %d %s", response.Code, response.Body)
				}
				var output JSON
				if err := json.Unmarshal(response.Body.Bytes(), &output); err != nil {
					t.Fatal(err)
				}
				if route.path == "/sync/accounts" {
					output = output["accounts"].([]interface{})[0].(map[string]interface{})
				}
				got, gotPresent := output["privateDetails"].(map[string]interface{})["alias"]
				want, wantPresent := details["alias"]
				if gotPresent != wantPresent || got != want {
					t.Fatalf("alias changed at HTTP boundary: got %#v, want %#v", output, details)
				}
			})
		}
	}
}

func TestAccountAliasContractRejectsInvalidTypesAndUnknownFields(t *testing.T) {
	router := accountAliasRouter(t, &Principal{ID: "owner", Permissions: []string{"self.accounts.write"}})
	cases := []struct {
		name, message string
		details       JSON
	}{
		{"null", "alias must be a string", JSON{"alias": nil}},
		{"number", "alias must be a string", JSON{"alias": 12}},
		{"boolean", "alias must be a string", JSON{"alias": true}},
		{"array", "alias must be a string", JSON{"alias": []interface{}{}}},
		{"object", "alias must be a string", JSON{"alias": JSON{}}},
		{"unknown", "property unexpected should not exist", JSON{"alias": "work", "unexpected": true}},
	}
	for _, test := range cases {
		for _, route := range accountAliasRoutes {
			t.Run(test.name+route.path, func(t *testing.T) {
				response := sendAccountAliasRequest(t, router, route, test.details)
				if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), test.message) {
					t.Fatalf("unexpected validation response: %d %s", response.Code, response.Body)
				}
			})
		}
	}
}

func TestAccountAliasContractRejectsUnauthorizedWrites(t *testing.T) {
	cases := []struct {
		name      string
		principal *Principal
		status    int
	}{
		{"unauthenticated", nil, http.StatusUnauthorized},
		{"read only", &Principal{ID: "owner", Permissions: []string{"self.accounts.read"}}, http.StatusForbidden},
	}
	for _, test := range cases {
		router := accountAliasRouter(t, test.principal)
		for _, route := range accountAliasRoutes {
			t.Run(test.name+route.path, func(t *testing.T) {
				response := sendAccountAliasRequest(t, router, route, JSON{"alias": "work"})
				if response.Code != test.status {
					t.Fatalf("unexpected authorization response: %d %s", response.Code, response.Body)
				}
			})
		}
	}
}

package accounts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
)

func TestHelpSettingsRequireCachedVisibleModelAndBoundedPrompt(t *testing.T) {
	catalog := json.RawMessage(`{"models":[{"slug":"official"},{"id":"alternate"},{"slug":"hidden","visibility":"hide"}]}`)
	for _, model := range []string{"official", "alternate"} {
		if err := validateHelpModel(model, catalog); err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []string{"hidden", "third-party", "", "Official"} {
		if validateHelpModel(model, catalog) == nil {
			t.Fatalf("accepted %q", model)
		}
	}
	for _, raw := range []string{"null", "{}", "[]", `{"models":[]}`} {
		if validateHelpModel("official", json.RawMessage(raw)) == nil {
			t.Fatal("empty catalog accepted")
		}
	}
	valid := helpSettings{Model: " official ", SystemPrompt: " 帮助用户 "}
	normalized, err := normalizeHelpSettings(valid)
	if err != nil || normalized.Model != "official" || normalized.SystemPrompt != "帮助用户" {
		t.Fatal(normalized, err)
	}
	for _, prompt := range []string{"", " ", strings.Repeat("界", maxHelpPromptChars+1)} {
		valid.SystemPrompt = prompt
		if _, err := normalizeHelpSettings(valid); err == nil {
			t.Fatal("invalid prompt accepted")
		}
	}
	valid.SystemPrompt = strings.Repeat("界", maxHelpPromptChars)
	if _, err := normalizeHelpSettings(valid); err != nil {
		t.Fatal(err)
	}
}

func TestHelpSettingsPermissionsAndStrictInput(t *testing.T) {
	deps := &platform.Dependencies{}
	deps.Authenticate = func(c *gin.Context) (*platform.Principal, error) {
		return &platform.Principal{ID: "reader", Permissions: []string{c.GetHeader("test-permission")}}, nil
	}
	router := gin.New()
	(&service{deps: deps}).registerHelpSettings(router)
	for _, test := range []struct {
		method, permission, body string
		status                   int
	}{
		{http.MethodGet, "", "", 403},
		{http.MethodPatch, "admin.official-accounts.read", `{}`, 403},
		{http.MethodPatch, "admin.official-accounts.manage", `{"model":"x","systemPrompt":"help","extra":true}`, 400},
		{http.MethodPatch, "admin.official-accounts.manage", `{"model":"x","systemPrompt":"help"} {}`, 400},
		{http.MethodPatch, "admin.official-accounts.manage", `{"model":"x","systemPrompt":"help"}`, 503},
	} {
		request := httptest.NewRequest(test.method, "/admin/api/help-assistant", strings.NewReader(test.body))
		request.Header.Set("test-permission", test.permission)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != test.status {
			t.Fatalf("%+v: %d %s", test, recorder.Code, recorder.Body.String())
		}
	}
}

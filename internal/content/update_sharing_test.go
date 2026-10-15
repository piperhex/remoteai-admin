package content

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/codex-switch/admin-go/internal/updatesharing"
	"github.com/gin-gonic/gin"
)

func TestUpdateSharingDefaultsAndValidation(t *testing.T) {
	defaults := updatesharing.Defaults()
	if defaults.Scope != updatesharing.Everyone || defaults.UploadKibPerSecond != 0 ||
		defaults.UnmeteredOnly || !defaults.PrepareAndroid {
		t.Fatal(defaults)
	}
	data, _ := json.Marshal(defaults)
	parsed, err := parseUpdateSharing(data)
	if err != nil || parsed != defaults {
		t.Fatal(parsed, err)
	}
	for _, payload := range []string{
		`{}`, `null`, `[]`,
		`{"scope":"everyone","uploadKibPerSecond":0,"unmeteredOnly":false}`,
		strings.Replace(string(data), `"everyone"`, `"invalid"`, 1),
		strings.Replace(string(data), `:0`, `:null`, 1),
		strings.Replace(string(data), `:0`, `:-1`, 1),
		strings.Replace(string(data), `:0`, `:1`, 1),
		strings.Replace(string(data), `:0`, `:1.5`, 1),
		strings.Replace(string(data), `:0`, `:8193`, 1),
		strings.Replace(string(data), `:false`, `:"false"`, 1),
		strings.Replace(string(data), `:true`, `:null`, 1),
		strings.Replace(string(data), `}`, `,"unknown":true}`, 1),
	} {
		if _, err := parseUpdateSharing([]byte(payload)); err == nil {
			t.Fatalf("accepted invalid settings: %s", payload)
		}
	}
	for _, scope := range []string{updatesharing.Off, updatesharing.Own, updatesharing.Everyone} {
		for _, speed := range []uint32{0, 64, 1024, 8192} {
			defaults.Scope, defaults.UploadKibPerSecond = scope, speed
			if err := defaults.Validate(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestUpdateSharingAdminPermissions(t *testing.T) {
	for _, test := range []struct {
		method      string
		permissions []string
		want        int
	}{
		{http.MethodGet, nil, http.StatusForbidden},
		{http.MethodPatch, []string{"admin.update-sharing.read"}, http.StatusForbidden},
		{http.MethodPatch, []string{"admin.update-sharing.manage"}, http.StatusBadRequest},
	} {
		router := gin.New()
		s := &service{deps: &platform.Dependencies{Authenticate: func(*gin.Context) (*platform.Principal, error) {
			return &platform.Principal{Role: "custom", Permissions: test.permissions}, nil
		}}}
		s.updateSharingRoutes(router)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(test.method, "/admin/api/update-sharing", strings.NewReader(`{}`)))
		if response.Code != test.want {
			t.Fatalf("%s: %d %s", test.method, response.Code, response.Body)
		}
	}
}

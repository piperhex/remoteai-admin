package sshprofiles

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestX11DisplayValidation(t *testing.T) {
	for _, display := range []string{"", ":0", "unix:1.2", "localhost:10", "127.0.0.1:59535.65535", "[::1]:0"} {
		value := fixture()
		value.X11 = &x11Options{Display: display}
		if validate(value) != nil {
			t.Fatalf("rejected local display %q", display)
		}
	}
	for _, display := range []string{"server.test:0", "192.168.1.2:0", ":59536", ":0.65536", ":0\n",
		"/tmp/x11:0", strings.Repeat("0", 257)} {
		value := fixture()
		value.X11 = &x11Options{Display: display}
		if validate(value) == nil {
			t.Fatalf("accepted invalid display %q", display)
		}
	}
}

func TestX11ProfileJSONRoundTrip(t *testing.T) {
	value := fixture()
	value.X11 = &x11Options{Display: "localhost:1"}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var restored profile
	if err := json.Unmarshal(body, &restored); err != nil || restored.X11 == nil ||
		restored.X11.Display != value.X11.Display {
		t.Fatalf("lost X11 settings: %s, %v", body, err)
	}
	for _, body := range []string{`{"x11":{"display":":0","cookie":"secret"}}`, `{"x11":{"display":1}}`} {
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		context.Request = httptest.NewRequest("PUT", "/", strings.NewReader(body))
		if decode(context, &restored) == nil {
			t.Fatal("accepted invalid X11 options")
		}
	}
}

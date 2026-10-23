package devices

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
)

func TestScreenPowerRejectsInvalidActionsBeforeForwarding(t *testing.T) {
	g := newControlGateway(nil)
	router := gin.New()
	router.POST("/devices/:deviceId/screen-power", g.screenPower)
	for _, body := range []string{`{}`, `{"action":true}`, `{"action":"toggle"}`, `{"action":"sleep"}`, `null`, `invalid`} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("POST", "/devices/device/screen-power", strings.NewReader(body)))
		if response.Code != 400 || len(g.pending) != 0 {
			t.Fatalf("invalid screen request accepted: %s", body)
		}
	}
	if !validScreenPowerAction("off") || !validScreenPowerAction("on") {
		t.Fatal("explicit screen power actions were rejected")
	}
}

func TestScreenPowerRequiresWindowsAndAdvertisedSupport(t *testing.T) {
	if !supportedCapability(screenPowerCapability) {
		t.Fatal("screen power capability cannot be registered")
	}
	device := &Device{Platform: "Windows", Capabilities: []string{screenPowerCapability}}
	if !supportsScreenPower(device) {
		t.Fatal("updated Windows host was rejected")
	}
	device.Capabilities = nil
	if supportsScreenPower(device) {
		t.Fatal("old host accepted a command it cannot handle")
	}
	device.Platform, device.Capabilities = "macOS", []string{screenPowerCapability}
	if supportsScreenPower(device) {
		t.Fatal("unsupported platform accepted screen power")
	}
}

func TestScreenPowerUsesOnlyOwnerDeviceChannelAndAwaitsAcknowledgement(t *testing.T) {
	g := newControlGateway(nil)
	desktop := queuedPeer()
	g.sockets["owner:computer"] = desktop
	command := platform.JSON{"type": screenPowerCapability, "action": "off"}
	if g.command("other-owner", "computer", command) == nil || len(desktop.queue) != 0 {
		t.Fatal("screen command crossed account boundary")
	}
	result := make(chan error, 1)
	go func() { result <- g.command("owner", "computer", command) }()
	select {
	case <-desktop.queue:
	case <-time.After(time.Second):
		t.Fatal("screen command was not delivered")
	}
	select {
	case <-result:
		t.Fatal("reported success before the host acknowledged the command")
	default:
	}
	g.mu.Lock()
	for _, pending := range g.pending {
		pending.result <- nil
	}
	g.mu.Unlock()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("acknowledgement did not finish the request")
	}
}

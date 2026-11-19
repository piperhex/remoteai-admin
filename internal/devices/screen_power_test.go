package devices

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piperhex/remoteai-admin/internal/platform"
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
	if !supportedCapability(screenPowerCapability) || !supportedCapability(screenPowerStatusCapability) {
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

func TestScreenPowerStatusUsesOnlyTheInteractiveHostResponse(t *testing.T) {
	g := newControlGateway(nil)
	desktop, service, other := queuedPeer(), queuedPeer(), queuedPeer()
	session := controlSession{owner: "owner", device: "computer", kind: "device"}
	g.sessions[desktop], g.sessions[other] = session, session
	target := commandTarget{owner: "owner", id: "computer", interactive: true, readOnly: true}
	service.serviceHost.Store(true)
	g.sockets["owner:computer"] = service
	command := platform.JSON{"type": screenPowerStatusCapability}
	if _, err := g.requestCommand(context.Background(), target, command); !errors.Is(err, errInteractiveHostRequired) {
		t.Fatalf("unattended service accepted: %v", err)
	}
	g.sockets["owner:computer"] = desktop
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan commandReply, 1)
	go func() {
		data, err := g.requestCommand(ctx, target, command)
		result <- commandReply{data: data, err: err}
	}()
	select {
	case <-desktop.queue:
	case <-ctx.Done():
		t.Fatal("status request was not delivered")
	}
	response := platform.JSON{"type": "switch-result", "commandId": command["commandId"],
		"success": true, "data": platform.JSON{"state": "on"}}
	if err := g.receive(other, response, nil); err != nil {
		t.Fatal(err)
	}
	response["data"] = platform.JSON{"state": "off"}
	if err := g.receive(desktop, response, nil); err != nil {
		t.Fatal(err)
	}
	reply := <-result
	if reply.err != nil || reply.data["state"] != "off" {
		t.Fatalf("unexpected display status: %+v", reply)
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
		pending.result <- commandReply{}
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

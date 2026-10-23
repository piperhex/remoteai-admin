package devices

import (
	"strings"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
)

const screenPowerCapability = "screen-power"

type screenPowerRequest struct {
	Action string `json:"action"`
}

func validScreenPowerAction(action string) bool { return action == "off" || action == "on" }

func supportsScreenPower(device *Device) bool {
	return strings.EqualFold(device.Platform, "windows") &&
		checkCapability(device, commandSpec{capability: screenPowerCapability}) == nil
}

// Screen power is an owner-authorized device command, independent of desktop streaming.
func (g *ControlGateway) screenPower(c *gin.Context) {
	var input screenPowerRequest
	if c.ShouldBindJSON(&input) != nil || !validScreenPowerAction(input.Action) {
		platform.Fail(c, 400, "client.deviceScreen.failed")
		return
	}
	owner, id := platform.User(c).ID, c.Param("deviceId")
	device, err := g.service.owned(owner, id)
	if err != nil {
		platform.Respond(c, nil, err)
		return
	}
	if !supportsScreenPower(device) {
		platform.Fail(c, 409, "client.deviceScreen.updateRequired")
		return
	}
	g.mu.Lock()
	client := g.sockets[owner+":"+id]
	interactive := client != nil && !client.closed.Load() && !client.serviceHost.Load()
	g.mu.Unlock()
	if !interactive {
		platform.Fail(c, 409, "client.deviceScreen.openComputerApp")
		return
	}
	if g.command(owner, id, platform.JSON{"type": screenPowerCapability, "action": input.Action}) != nil {
		platform.Fail(c, 409, "client.deviceScreen.failed")
		return
	}
	c.Status(204)
}

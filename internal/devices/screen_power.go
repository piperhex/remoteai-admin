package devices

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/piperhex/remoteai-admin/internal/platform"
)

const screenPowerCapability = "screen-power"
const screenPowerStatusCapability = "screen-power-status"
const screenPowerStatusTimeout = 4 * time.Second

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
	target := commandTarget{owner: platform.User(c).ID, id: c.Param("deviceId"), interactive: true}
	if err := g.checkScreenPowerTarget(target); err != nil {
		platform.Respond(c, nil, err)
		return
	}
	if _, err := g.requestCommand(c.Request.Context(), target,
		platform.JSON{"type": screenPowerCapability, "action": input.Action}); err != nil {
		screenPowerFailed(c, err)
		return
	}
	c.Status(204)
}

func (g *ControlGateway) screenPowerStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	target := commandTarget{owner: platform.User(c).ID, id: c.Param("deviceId"), interactive: true, readOnly: true}
	if err := g.checkScreenPowerTarget(target); err != nil {
		platform.Respond(c, nil, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), screenPowerStatusTimeout)
	defer cancel()
	data, err := g.requestCommand(ctx, target, platform.JSON{"type": screenPowerStatusCapability})
	if err != nil {
		screenPowerFailed(c, err)
		return
	}
	state, _ := data["state"].(string)
	if !validScreenPowerAction(state) {
		platform.Fail(c, 409, "client.deviceScreen.failed")
		return
	}
	c.JSON(200, gin.H{"state": state})
}

func (g *ControlGateway) checkScreenPowerTarget(target commandTarget) error {
	device, err := g.service.owned(target.owner, target.id)
	if err != nil {
		return err
	}
	if !supportsScreenPower(device) || (target.readOnly &&
		checkCapability(device, commandSpec{capability: screenPowerStatusCapability}) != nil) {
		return platform.NewError(409, "client.deviceScreen.updateRequired")
	}
	return nil
}

func screenPowerFailed(c *gin.Context, err error) {
	if errors.Is(err, errInteractiveHostRequired) {
		platform.Fail(c, 409, "client.deviceScreen.openComputerApp")
		return
	}
	platform.Fail(c, 409, "client.deviceScreen.failed")
}

package devices

import (
	"net/mail"
	"strings"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (g *ChatGateway) registerAssistance(router *gin.Engine) {
	group := router.Group("/remote-assistance", g.service.deps.RequireAuth())
	group.GET("", g.assistanceList)
	group.POST("", g.assistanceCreate)
	group.POST("/:id", g.assistanceUpdate)
}

func (g *ChatGateway) assistanceList(c *gin.Context) {
	owner, device := platform.User(c).ID, c.Query("deviceId")
	if _, err := g.service.owned(owner, device); err != nil {
		platform.Respond(c, nil, err)
		return
	}
	platform.Respond(c, gin.H{"requests": g.sessions.listAssistance(owner, device)}, nil)
}

func (g *ChatGateway) assistanceCreate(c *gin.Context) {
	var input struct {
		DeviceID string `json:"deviceId" binding:"required,max=160"`
		Email    string `json:"email" binding:"max=254"`
		UserID   string `json:"userId" binding:"omitempty,uuid"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		platform.Fail(c, 400, "请输入有效的邮箱地址。")
		return
	}
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if !validAssistanceRecipient(email, input.UserID) {
		platform.Fail(c, 400, "请输入有效的邮箱地址。")
		return
	}
	user := platform.User(c)
	device, err := g.service.owned(user.ID, input.DeviceID)
	if err != nil {
		platform.Respond(c, nil, err)
		return
	}
	helper, label, err := g.assistanceRecipient(email, input.UserID)
	if err != nil {
		platform.Respond(c, nil, err)
		return
	}
	if helper == user.ID {
		helper = ""
	}
	hostLabel := user.Email
	if input.UserID != "" {
		// Chat invitations resolve by stable ID, never by a potentially shared prefix.
		hostLabel, _, _ = strings.Cut(hostLabel, "@")
		email = label
	}
	request, err := g.sessions.createAssistance(assistanceRequest{
		HostOwner: user.ID, HostDeviceID: device.DeviceID, HostName: device.Name, HostEmail: hostLabel,
		HelperOwner: helper, HelperEmail: email,
	})
	platform.Respond(c, request, err)
}

func validAssistanceRecipient(email, userID string) bool {
	if userID != "" {
		_, err := uuid.Parse(userID)
		return email == "" && err == nil
	}
	parsed, err := mail.ParseAddress(email)
	return err == nil && parsed.Address == email
}

func (g *ChatGateway) assistanceRecipient(email, userID string) (string, string, error) {
	var user struct{ ID, Label string }
	query := g.service.deps.DB.Table("users").Select("id, split_part(email, '@', 1) AS label").Where("disabled = ?", false)
	if userID != "" {
		query = query.Where("id = ?", userID)
	} else {
		query = query.Where("email = ?", email)
	}
	result := query.Scan(&user)
	if result.Error != nil {
		return "", "", result.Error
	}
	return user.ID, user.Label, nil
}

func (g *ChatGateway) assistanceUpdate(c *gin.Context) {
	var input struct {
		DeviceID string `json:"deviceId" binding:"required,max=160"`
		Action   string `json:"action" binding:"required,oneof=accept decline end"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		platform.Fail(c, 400, "请选择有效的协助操作。")
		return
	}
	owner := platform.User(c).ID
	if _, err := g.service.owned(owner, input.DeviceID); err != nil {
		platform.Respond(c, nil, err)
		return
	}
	request, err := g.sessions.updateAssistance(assistanceActor{owner, input.DeviceID, c.Param("id")}, input.Action)
	platform.Respond(c, request, err)
}

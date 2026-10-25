package sshprofiles

import (
	"errors"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type saveRequest struct {
	profile
	Credentials *credentials `json:"credentials,omitempty"`
}

func saveProfile(c *gin.Context, deps *platform.Dependencies) {
	var request saveRequest
	if decode(c, &request) != nil || request.ID != c.Param("id") || validate(request.profile) != nil ||
		(request.Credentials != nil && !request.Credentials.valid(request.Auth)) {
		platform.Respond(c, nil, invalid())
		return
	}
	owner := platform.User(c).ID
	edit := change{Value: request.profile}
	if request.Credentials != nil {
		sealed, err := sealCredentials(deps.Config, owner, request.profile, *request.Credentials)
		if err != nil {
			platform.Respond(c, nil, err)
			return
		}
		edit.EncryptedCredentials = &sealed
	}
	result, err := mutate(deps.DB.WithContext(c.Request.Context()), owner, edit)
	platform.Respond(c, result, err)
}

// Credentials are fetched only for an explicit connection attempt, never in the synchronized list.
func fetchCredentials(c *gin.Context, deps *platform.Dependencies) {
	var request struct {
		Revision int64 `json:"revision"`
	}
	if decode(c, &request) != nil || !profileID.MatchString(c.Param("id")) || request.Revision <= 0 ||
		request.Revision > 9007199254740990 {
		platform.Respond(c, nil, invalid())
		return
	}
	var row record
	err := deps.DB.WithContext(c.Request.Context()).Where(`"ownerId" = ? AND "profileId" = ?`,
		platform.User(c).ID, c.Param("id")).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && (row.Config == nil || row.Version != request.Revision)) {
		err = conflict()
	}
	if err != nil {
		platform.Respond(c, nil, err)
		return
	}
	secret, err := openCredentials(deps.Config, row)
	platform.Respond(c, gin.H{"credentials": secret}, err)
}

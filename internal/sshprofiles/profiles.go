// Package sshprofiles stores account-owned SSH connection metadata, never credentials.
package sshprofiles

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
)

const maxProfiles = 64
const maxBodyBytes = 256 * 1024

var profileID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

type profile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Auth        string `json:"auth"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Revision    int64  `json:"revision,omitempty"`
}

func invalid() error  { return platform.NewError(400, "client.ssh.invalid") }
func conflict() error { return platform.NewError(409, "client.ssh.profileChanged") }

func validText(value string, limit int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func validate(value profile) error {
	if !profileID.MatchString(value.ID) || !validText(value.Name, 80) ||
		!validText(value.Host, 253) || strings.TrimSpace(value.Host) == "" ||
		strings.IndexFunc(value.Host, unicode.IsSpace) >= 0 ||
		!validText(value.Username, 256) || strings.TrimSpace(value.Username) == "" ||
		!validText(value.Fingerprint, 256) || value.Port < 1 || value.Port > 65535 ||
		(value.Auth != "password" && value.Auth != "key") || value.Revision < 0 || value.Revision > 9007199254740990 {
		return invalid()
	}
	return nil
}

// Register uses the account JWT independently of any computer connection or account-provider permissions.
func Register(router *gin.Engine, deps *platform.Dependencies) error {
	r := router.Group("/sync/ssh-profiles", deps.RequireAuth(), func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
	})
	r.GET("", func(c *gin.Context) {
		result, err := list(deps.DB.WithContext(c.Request.Context()), platform.User(c).ID)
		platform.Respond(c, result, err)
	})
	r.PUT("/:id", func(c *gin.Context) {
		var value profile
		if decode(c, &value) != nil || value.ID != c.Param("id") || validate(value) != nil {
			platform.Respond(c, nil, invalid())
			return
		}
		result, err := mutate(deps.DB.WithContext(c.Request.Context()), platform.User(c).ID, change{Value: value})
		platform.Respond(c, result, err)
	})
	r.DELETE("/:id", func(c *gin.Context) {
		var body struct {
			Revision int64 `json:"revision"`
		}
		if decode(c, &body) != nil || !profileID.MatchString(c.Param("id")) || body.Revision <= 0 {
			platform.Respond(c, nil, invalid())
			return
		}
		value := profile{ID: c.Param("id"), Revision: body.Revision}
		result, err := mutate(deps.DB.WithContext(c.Request.Context()), platform.User(c).ID,
			change{Value: value, Delete: true})
		platform.Respond(c, result, err)
	})
	r.POST("/import", func(c *gin.Context) { importProfiles(c, deps) })
	return nil
}

func decode(c *gin.Context, value interface{}) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes)
	decoder := json.NewDecoder(c.Request.Body)
	// Unknown fields include passwords, keys and ownership claims; never persist them.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return invalid()
	}
	if decoder.Decode(new(interface{})) != io.EOF {
		return invalid()
	}
	return nil
}

func importProfiles(c *gin.Context, deps *platform.Dependencies) {
	var body struct {
		Profiles []profile `json:"profiles"`
	}
	if decode(c, &body) != nil || body.Profiles == nil || len(body.Profiles) > maxProfiles {
		platform.Respond(c, nil, invalid())
		return
	}
	seen := map[string]bool{}
	for _, value := range body.Profiles {
		if validate(value) != nil || seen[value.ID] {
			platform.Respond(c, nil, invalid())
			return
		}
		seen[value.ID] = true
	}
	result, err := migrate(deps.DB.WithContext(c.Request.Context()), platform.User(c).ID, body.Profiles)
	platform.Respond(c, result, err)
}

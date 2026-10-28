package lounge

import (
	"crypto/rand"
	"net/http"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const ticketLifetime = 10 * time.Minute

type claims struct {
	Account string `json:"account"`
	jwt.RegisteredClaims
}

// Each process owns an ephemeral signing key. Restarting invalidates tickets, never replays messages.
func Register(router *gin.Engine, deps *platform.Dependencies) (func(), error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	hub := newHub(key)
	hub.policies = postgresPolicies{db: deps.DB}
	hub.registerModeration(router, deps)
	router.POST("/lounge/ticket", deps.RequireAuth(), hub.ticket)
	router.GET("/lounge/socket", hub.serve)
	return hub.close, nil
}

func (h *hub) ticket(c *gin.Context) {
	user := platform.User(c)
	expires := time.Now().Add(ticketLifetime)
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		Account: user.Email,
		RegisteredClaims: jwt.RegisteredClaims{Subject: user.ID, Audience: []string{"lounge"},
			ExpiresAt: jwt.NewNumericDate(expires)},
	}).SignedString(h.key)
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"ticket": token})
}

func (h *hub) authenticate(ticket string) (account, time.Time, error) {
	var value claims
	if len(ticket) > 8192 {
		return account{}, time.Time{}, errInvalid
	}
	_, err := jwt.ParseWithClaims(ticket, &value, func(*jwt.Token) (interface{}, error) { return h.key, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience("lounge"), jwt.WithExpirationRequired())
	if err != nil || value.Subject == "" || value.Account == "" {
		return account{}, time.Time{}, errInvalid
	}
	return account{ID: value.Subject, Account: value.Account}, value.ExpiresAt.Time, nil
}

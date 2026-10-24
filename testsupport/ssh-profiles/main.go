// Disposable loopback fixture for the real SSH profile API. Requires an isolated test database.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/codex-switch/admin-go/internal/migrations"
	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/codex-switch/admin-go/internal/sshprofiles"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const owner = "00000000-0000-4000-8000-000000000101"

func main() {
	dsn := os.Getenv("SSH_PROFILES_TEST_DSN")
	if dsn == "" {
		log.Fatal("Set SSH_PROFILES_TEST_DSN to an isolated disposable database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal(err)
	}
	if err := migrations.InitializeEmpty(db); err != nil {
		log.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO users(id,email,"passwordHash") VALUES(?,?,?) ON CONFLICT DO NOTHING`,
		owner, "ssh-fixture@example.test", "fixture-only").Error; err != nil {
		log.Fatal(err)
	}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "http://127.0.0.1:1488")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Header("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, OPTIONS")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
		}
	})
	deps := &platform.Dependencies{DB: db, Authenticate: func(c *gin.Context) (*platform.Principal, error) {
		if c.GetHeader("Authorization") != "Bearer ssh-fixture" {
			return nil, nil
		}
		return &platform.Principal{ID: owner, Email: "ssh-fixture@example.test"}, nil
	}}
	if err := sshprofiles.Register(r, deps); err != nil {
		log.Fatal(err)
	}
	r.POST("/fixture/reset", deps.RequireAuth(), func(c *gin.Context) {
		err := db.Exec(`DELETE FROM synced_ssh_profiles WHERE "ownerId" = ?`, owner).Error
		platform.Respond(c, gin.H{"reset": err == nil}, err)
	})
	log.Fatal(http.ListenAndServe("127.0.0.1:1499", r))
}

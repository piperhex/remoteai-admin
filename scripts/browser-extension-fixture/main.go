// A loopback-only UI fixture with real PostgreSQL persistence and production extension handlers.
package main

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/piperhex/remoteai-admin/internal/browserextensions"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	dsn := os.Getenv("BROWSER_EXTENSIONS_TEST_DATABASE_URL")
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Hostname() != "127.0.0.1" || parsed.Path != "/extensions_test" {
		log.Fatal("use the isolated loopback extensions_test fixture database")
	}
	db, err := gorm.Open(postgres.Open(dsn))
	if err != nil {
		log.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		log.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	schema := "extensions_ui_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	for _, query := range []string{`CREATE SCHEMA "` + schema + `"`, `SET search_path TO "` + schema + `"`} {
		if err := db.Exec(query).Error; err != nil {
			log.Fatal(err)
		}
	}
	data, err := os.ReadFile("sql/20261115-browser-extensions.sql")
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Exec(strings.ReplaceAll(string(data), "public.", "")).Error; err != nil {
		log.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE admin_audit_logs (id text, "actorId" text, "actorEmail" text,
		action text, "targetType" text, "targetId" text, "createdAt" timestamptz)`).Error; err != nil {
		log.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Headers", "Authorization,Content-Type")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,HEAD,OPTIONS")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})
	deps := &platform.Dependencies{DB: db, Authenticate: func(c *gin.Context) (*platform.Principal, error) {
		if c.GetHeader("Authorization") != "Bearer extension-fixture" {
			return nil, nil
		}
		return &platform.Principal{ID: "extension-fixture", Email: "extensions@fixture.test", Role: "admin",
			Permissions: []string{"admin.browser-extensions.read", "admin.browser-extensions.manage"}}, nil
	}}
	if err := browserextensions.Register(router, deps); err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: "127.0.0.1:1592", Handler: router, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(server.ListenAndServe())
}

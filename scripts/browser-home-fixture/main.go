// A loopback-only UI fixture with real PostgreSQL persistence and production home handlers.
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
	"github.com/piperhex/remoteai-admin/internal/browserhome"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	dsn := os.Getenv("BROWSER_HOME_TEST_DATABASE_URL")
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Hostname() != "127.0.0.1" || parsed.Path != "/browser_home" {
		log.Fatal("use the isolated loopback browser_home fixture database")
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
	schema := "home_ui_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	for _, query := range []string{`CREATE SCHEMA "` + schema + `"`, `SET search_path TO "` + schema + `"`} {
		if err := db.Exec(query).Error; err != nil {
			log.Fatal(err)
		}
	}
	data, err := os.ReadFile("sql/20261114-browser-home.sql")
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
		c.Header("Access-Control-Allow-Methods", "GET,POST,PATCH,HEAD,OPTIONS")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})
	deps := &platform.Dependencies{DB: db, Authenticate: func(c *gin.Context) (*platform.Principal, error) {
		if c.GetHeader("Authorization") != "Bearer home-fixture" {
			return nil, nil
		}
		return &platform.Principal{ID: "home-fixture", Email: "home@fixture.test", Role: "admin",
			Permissions: []string{"admin.browser-home.read", "admin.browser-home.manage"}}, nil
	}}
	if err := browserhome.Register(router, deps); err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: "127.0.0.1:1591", Handler: router, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(server.ListenAndServe())
}

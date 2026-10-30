package content

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
)

func TestSkillCatalogIncludesEntriesBeyondTwoHundred(t *testing.T) {
	db := sharingDatabase(t)
	if err := db.AutoMigrate(&SkillMarketItem{}); err != nil {
		t.Fatal(err)
	}
	const count = 205
	items := make([]SkillMarketItem, count)
	for index := range items {
		items[index] = SkillMarketItem{Id: fmt.Sprint(index), Title: fmt.Sprintf("Skill %d", index),
			ArchiveData: []byte("archive"), PreviewData: []byte("preview"), UploaderEmail: "private@example.test",
			CreatedAt: time.Unix(int64(index+1), 0)}
	}
	if err := db.CreateInBatches(items, 25).Error; err != nil {
		t.Fatal(err)
	}
	s := &service{deps: &platform.Dependencies{DB: db}}
	router := gin.New()
	router.GET("/skills", s.listSkills)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/skills", nil))
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body)
	}
	var body struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != count || body.Items[0]["id"] != "204" || body.Items[count-1]["id"] != "0" {
		t.Fatalf("catalog omitted or reordered entries: count=%d", len(body.Items))
	}
	for _, item := range body.Items {
		for _, field := range []string{"archiveData", "previewData", "uploaderEmail"} {
			if _, exists := item[field]; exists {
				t.Fatalf("catalog exposed %s", field)
			}
		}
	}
}

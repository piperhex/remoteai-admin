package accounts

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const maxAggregateRecords = 1000
const maxAggregatePayloadBytes = 4 * 1024 * 1024

var aggregateID = regexp.MustCompile(`^[a-zA-Z0-9-]{1,64}$`)

type aggregateRecord struct {
	OwnerID    string    `gorm:"column:ownerId;primaryKey" json:"-"`
	ID         string    `gorm:"column:aggregateId;primaryKey" json:"id"`
	ModifiedAt time.Time `gorm:"column:modifiedAt" json:"modifiedAt"`
	Config     object    `gorm:"column:config;serializer:json" json:"config"`
}

func (aggregateRecord) TableName() string { return "synced_aggregates" }

type aggregatePayload struct {
	Aggregates []aggregateRecord `json:"aggregates"`
}

type aggregateConfiguration struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Model             string            `json:"model"`
	MemberProviderIDs []string          `json:"memberProviderIds"`
	MemberModels      map[string]string `json:"memberModels"`
	Enabled           bool              `json:"enabled"`
}

func (s *service) registerAggregates(r *gin.RouterGroup) {
	r.GET("/aggregates", s.deps.RequirePermissions("self.providers.read"), noStore,
		endpoint(func(c *gin.Context) (interface{}, error) { return listAggregates(s.deps.DB, platform.User(c).ID) }))
	r.PUT("/aggregates", s.deps.RequirePermissions("self.providers.read", "self.providers.write"), noStore,
		endpoint(func(c *gin.Context) (interface{}, error) {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAggregatePayloadBytes)
			var incoming aggregatePayload
			if err := c.ShouldBindJSON(&incoming); err != nil {
				return nil, invalidAggregate()
			}
			if err := validateAggregates(incoming); err != nil {
				return nil, err
			}
			return s.mergeAggregates(platform.User(c).ID, incoming)
		}))
}

func invalidAggregate() error { return platform.NewError(400, "Invalid aggregate configuration") }

func validateAggregates(payload aggregatePayload) error {
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > maxAggregatePayloadBytes {
		return invalidAggregate()
	}
	if len(payload.Aggregates) > maxAggregateRecords {
		return invalidAggregate()
	}
	seen := map[string]bool{}
	for _, entry := range payload.Aggregates {
		if !aggregateID.MatchString(entry.ID) || seen[entry.ID] || entry.ModifiedAt.IsZero() ||
			entry.ModifiedAt.After(time.Now().Add(5*time.Minute)) {
			return invalidAggregate()
		}
		seen[entry.ID] = true
		if entry.Config != nil {
			if err := validateAggregateConfiguration(entry); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateAggregateConfiguration(entry aggregateRecord) error {
	if _, valid := entry.Config["memberProviderIds"].([]interface{}); !valid {
		return invalidAggregate()
	}
	if value, present := entry.Config["memberModels"]; present {
		if _, valid := value.(map[string]interface{}); !valid {
			return invalidAggregate()
		}
	}
	encoded, err := json.Marshal(entry.Config)
	if err != nil || len(encoded) > 64*1024 {
		return invalidAggregate()
	}
	var config aggregateConfiguration
	if json.Unmarshal(encoded, &config) != nil || config.ID != entry.ID ||
		strings.TrimSpace(config.Name) == "" || len(config.Name) > 256 ||
		strings.TrimSpace(config.Model) == "" || len(config.Model) > 256 ||
		strings.IndexFunc(config.Name, unicode.IsControl) >= 0 || strings.IndexFunc(config.Model, unicode.IsControl) >= 0 {
		return invalidAggregate()
	}
	if len(config.MemberProviderIDs) > 100 || (config.Enabled && len(config.MemberProviderIDs) < 2) {
		return invalidAggregate()
	}
	seen := map[string]bool{}
	for _, id := range config.MemberProviderIDs {
		if !aggregateID.MatchString(strings.TrimPrefix(id, "account:")) || seen[id] {
			return invalidAggregate()
		}
		seen[id] = true
	}
	for id, model := range config.MemberModels {
		if !seen[id] || strings.TrimSpace(model) == "" || len(model) > 256 || strings.IndexFunc(model, unicode.IsControl) >= 0 {
			return invalidAggregate()
		}
	}
	if _, valid := entry.Config["enabled"].(bool); !valid {
		return invalidAggregate()
	}
	if value, present := entry.Config["failoverPolicy"]; present {
		policy, valid := value.(map[string]interface{})
		if !valid {
			return invalidAggregate()
		}
		return validateAggregatePolicy(policy)
	}
	return nil
}

func listAggregates(db *gorm.DB, owner string) (aggregatePayload, error) {
	rows := []aggregateRecord{}
	err := db.Where(`"ownerId" = ?`, owner).Order(`"aggregateId"`).Limit(maxAggregateRecords + 1).Find(&rows).Error
	if len(rows) > maxAggregateRecords {
		return aggregatePayload{}, invalidAggregate()
	}
	return aggregatePayload{Aggregates: rows}, err
}

func shouldReplaceAggregate(local, incoming aggregateRecord) bool {
	return incoming.ModifiedAt.After(local.ModifiedAt) ||
		(incoming.ModifiedAt.Equal(local.ModifiedAt) && incoming.Config == nil && local.Config != nil)
}

func (s *service) mergeAggregates(owner string, incoming aggregatePayload) (aggregatePayload, error) {
	var result aggregatePayload
	err := s.deps.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "aggregate:"+owner).Error; err != nil {
			return err
		}
		local, err := listAggregates(tx, owner)
		if err != nil {
			return err
		}
		merged := map[string]aggregateRecord{}
		for _, row := range local.Aggregates {
			merged[row.ID] = row
		}
		for _, row := range incoming.Aggregates {
			existing, found := merged[row.ID]
			if found && !shouldReplaceAggregate(existing, row) {
				continue
			}
			row.OwnerID = owner
			merged[row.ID] = row
			if len(merged) > maxAggregateRecords {
				return invalidAggregate()
			}
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
		}
		result, err = listAggregates(tx, owner)
		if err != nil {
			return err
		}
		return validateAggregates(result)
	})
	return result, err
}

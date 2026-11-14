package browserextensions

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Item struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     string    `json:"version"`
	Icon        string    `json:"icon"`
	Permissions []string  `json:"permissions" gorm:"serializer:json"`
	SHA256      string    `json:"sha256" gorm:"column:sha256"`
	Size        int64     `json:"size"`
	Published   bool      `json:"published"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (Item) TableName() string { return "browser_extensions" }

type Package struct {
	ID   string `gorm:"primaryKey"`
	Data []byte
}

func (Package) TableName() string { return "browser_extension_packages" }

type Store interface {
	List(context.Context, bool) ([]Item, error)
	Upload(context.Context, Upload, *platform.Principal) (Item, error)
	Publish(context.Context, string, bool, *platform.Principal) (Item, error)
	Delete(context.Context, string, *platform.Principal) error
	Download(context.Context, string, string) ([]byte, error)
}
type databaseStore struct{ db *gorm.DB }

func (s databaseStore) List(ctx context.Context, public bool) ([]Item, error) {
	items := []Item{}
	query := s.db.WithContext(ctx).Order("updated_at DESC, id ASC")
	if public {
		query = query.Where("published = ?", true)
	}
	err := query.Find(&items).Error
	return items, err
}

func (s databaseStore) Upload(ctx context.Context, upload Upload, actor *platform.Principal) (Item, error) {
	item := upload.Item
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if item.ID == "" {
			item.ID = uuid.NewString()
		} else {
			var previous Item
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&previous, "id = ?", item.ID).Error; err != nil {
				return err
			}
		}
		// Replacements require a new publishing decision, including when permissions change.
		item.Published = false
		if err := tx.Save(&item).Error; err != nil {
			return err
		}
		if err := tx.Save(&Package{ID: item.ID, Data: upload.Data}).Error; err != nil {
			return err
		}
		return audit(tx, actor, "browser-extensions.upload", item.ID)
	})
	return item, err
}

func (s databaseStore) Publish(ctx context.Context, id string, published bool, actor *platform.Principal) (Item, error) {
	var item Item
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&item, "id = ?", id).Error; err != nil {
			return err
		}
		item.Published = published
		if err := tx.Save(&item).Error; err != nil {
			return err
		}
		return audit(tx, actor, "browser-extensions.publish", id)
	})
	return item, err
}

func (s databaseStore) Delete(ctx context.Context, id string, actor *platform.Principal) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&Item{}, "id = ?", id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return audit(tx, actor, "browser-extensions.delete", id)
	})
}

func (s databaseStore) Download(ctx context.Context, id, digest string) ([]byte, error) {
	var pack Package
	err := s.db.WithContext(ctx).Table("browser_extension_packages AS p").Select("p.*").
		Joins("JOIN browser_extensions AS e ON e.id = p.id").
		Where("e.id = ? AND e.sha256 = ? AND e.published = ?", id, digest, true).Take(&pack).Error
	return pack.Data, err
}

func audit(tx *gorm.DB, actor *platform.Principal, action, id string) error {
	return tx.Table("admin_audit_logs").Create(map[string]interface{}{
		"id": uuid.NewString(), "actorId": actor.ID, "actorEmail": actor.Email,
		"action": action, "targetType": "browser-extension", "targetId": id, "createdAt": time.Now(),
	}).Error
}

package browserhome

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Settings struct {
	ID         string    `json:"-" gorm:"primaryKey"`
	Enabled    bool      `json:"enabled"`
	Revision   string    `json:"revision"`
	MD5        string    `json:"md5" gorm:"column:md5"`
	FileName   string    `json:"fileName"`
	FileCount  int       `json:"fileCount"`
	TotalBytes int64     `json:"totalBytes"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

func (Settings) TableName() string { return "browser_home_settings" }

type Store interface {
	Read(context.Context) (Settings, error)
	Upload(context.Context, []Asset, string, *platform.Principal) (Settings, error)
	Enable(context.Context, bool, *platform.Principal) (Settings, error)
	Asset(context.Context, string, string) (Asset, error)
	Package(context.Context, string) (Package, error)
}

type Package struct {
	MD5  string `gorm:"primaryKey;column:md5"`
	Data []byte
}

func (Package) TableName() string { return "browser_home_packages" }

type Bundle struct {
	Revision string  `json:"revision"`
	Files    []Asset `json:"files"`
}

func (s databaseStore) Package(ctx context.Context, digest string) (Package, error) {
	var value Package
	err := s.db.WithContext(ctx).First(&value, "md5 = ?", digest).Error
	return value, err
}

func prepareBundle(assets []Asset) (Bundle, Package, error) {
	sort.Slice(assets, func(i, j int) bool { return assets[i].Path < assets[j].Path })
	original, err := json.Marshal(assets)
	if err != nil {
		return Bundle{}, Package{}, err
	}
	// MD5 is a cache validator, never an authentication or authorization boundary.
	revision := uuid.UUID(md5.Sum(original)).String()
	for i := range assets {
		assets[i].Revision = revision
		assets[i].Data = rebaseAsset(assets[i], assets)
	}
	bundle := Bundle{Revision: revision, Files: assets}
	data, err := json.Marshal(bundle)
	if len(data) > 160<<20 {
		return Bundle{}, Package{}, errInvalidArchive
	}
	digest := md5.Sum(data)
	return bundle, Package{MD5: hex.EncodeToString(digest[:]), Data: data}, err
}

type databaseStore struct{ db *gorm.DB }

func (s databaseStore) Read(ctx context.Context) (Settings, error) {
	settings := Settings{ID: "current"}
	err := s.db.WithContext(ctx).First(&settings, "id = ?", settings.ID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = nil
	}
	return settings, err
}

func (s databaseStore) Asset(ctx context.Context, revision, name string) (Asset, error) {
	var asset Asset
	err := s.db.WithContext(ctx).First(&asset, "revision = ? AND path = ?", revision, name).Error
	return asset, err
}

func lockSettings(tx *gorm.DB) (Settings, error) {
	settings := Settings{ID: "current"}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&settings).Error; err != nil {
		return settings, err
	}
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&settings, "id = ?", settings.ID).Error
	return settings, err
}

func (s databaseStore) Upload(ctx context.Context, assets []Asset, name string, actor *platform.Principal) (Settings, error) {
	var saved Settings
	bundle, archive, err := prepareBundle(assets)
	if err != nil {
		return saved, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		settings, err := lockSettings(tx)
		if err != nil {
			return err
		}
		previous, previousMD5 := settings.Revision, settings.MD5
		settings.Revision, settings.MD5, settings.FileName = bundle.Revision, archive.MD5, name
		settings.FileCount, settings.TotalBytes = len(assets), 0
		for i := range assets {
			settings.TotalBytes += int64(len(assets[i].Data))
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(assets, 50).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&archive).Error; err != nil {
			return err
		}
		if err := tx.Save(&settings).Error; err != nil {
			return err
		}
		// Retain one previous version so an already open home can finish loading its chunks.
		if err := tx.Where("revision NOT IN ?", []string{previous, settings.Revision}).Delete(&Asset{}).Error; err != nil {
			return err
		}
		if err := tx.Where("md5 NOT IN ?", []string{previousMD5, archive.MD5}).Delete(&Package{}).Error; err != nil {
			return err
		}
		saved = settings
		return auditChange(tx, actor, "browser-home.upload", settings)
	})
	return saved, err
}

var errNoUpload = errors.New("browser home has no upload")

func (s databaseStore) Enable(ctx context.Context, enabled bool, actor *platform.Principal) (Settings, error) {
	var saved Settings
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		settings, err := lockSettings(tx)
		if err != nil {
			return err
		}
		if enabled && settings.Revision == "" {
			return errNoUpload
		}
		settings.Enabled = enabled
		if err := tx.Save(&settings).Error; err != nil {
			return err
		}
		saved = settings
		return auditChange(tx, actor, "browser-home.update", settings)
	})
	return saved, err
}

func auditChange(tx *gorm.DB, actor *platform.Principal, action string, settings Settings) error {
	return tx.Table("admin_audit_logs").Create(map[string]interface{}{
		"id": uuid.NewString(), "actorId": actor.ID, "actorEmail": actor.Email,
		"action": action, "targetType": "browser-home", "targetId": settings.Revision,
		"createdAt": time.Now(),
	}).Error
}

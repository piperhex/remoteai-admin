// Package updatesharing owns the centrally managed package sharing policy.
package updatesharing

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

const (
	Off                   = "off"
	Own                   = "own"
	Everyone              = "everyone"
	MaxUploadKibPerSecond = 8192
)

// Settings applies to all clients. Zero upload speed means unlimited.
type Settings struct {
	Scope              string `json:"scope"`
	UploadKibPerSecond uint32 `json:"uploadKibPerSecond"`
	UnmeteredOnly      bool   `json:"unmeteredOnly"`
	PrepareAndroid     bool   `json:"prepareAndroid"`
}

type Record struct {
	ID        string    `gorm:"column:id;primaryKey"`
	Policy    Settings  `gorm:"column:policy;serializer:json"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (Record) TableName() string { return "update_sharing_settings" }

func Defaults() Settings {
	return Settings{Scope: Everyone, PrepareAndroid: true}
}

func (s Settings) Validate() error {
	if s.Scope != Off && s.Scope != Own && s.Scope != Everyone {
		return errors.New("invalid sharing scope")
	}
	if s.UploadKibPerSecond != 0 &&
		(s.UploadKibPerSecond < 64 || s.UploadKibPerSecond > MaxUploadKibPerSecond) {
		return errors.New("invalid upload speed")
	}
	return nil
}

func Read(db *gorm.DB) (Settings, error) {
	row := Record{ID: "current", Policy: Defaults()}
	err := db.First(&row, "id = ?", row.ID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Defaults(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	return row.Policy, row.Policy.Validate()
}

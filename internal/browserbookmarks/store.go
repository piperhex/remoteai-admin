package browserbookmarks

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Repository interface {
	Exchange(context.Context, string, *Document) (Document, bool, error)
}

type Postgres struct{ DB *gorm.DB }
type record struct {
	Owner    string  `gorm:"column:owner_id;primaryKey"`
	Revision int64   `gorm:"column:revision"`
	Entries  []Entry `gorm:"column:entries;serializer:json"`
}

func (record) TableName() string { return "browser_bookmarks" }

// Exchange holds the owner lock through the entire compare-and-swap transaction.
func (p Postgres) Exchange(ctx context.Context, owner string, edit *Document) (Document, bool, error) {
	result, accepted := Document{Entries: []Entry{}}, false
	if edit != nil && Validate(*edit) != nil {
		return result, false, ErrInvalid
	}
	db := p.DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).WithContext(ctx)
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "browser-bookmarks:"+owner).Error; err != nil {
			return err
		}
		row := record{Owner: owner, Entries: []Entry{}}
		err := tx.Where("owner_id = ?", owner).First(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if edit != nil && edit.Revision == row.Revision {
			row.Revision++
			row.Entries = edit.Entries
			if err := tx.Save(&row).Error; err != nil {
				return err
			}
			accepted = true
		}
		result = Document{Revision: row.Revision, Entries: row.Entries}
		return nil
	})
	return result, accepted, err
}

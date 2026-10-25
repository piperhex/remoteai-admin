package sshprofiles

import (
	"errors"

	"github.com/codex-switch/admin-go/internal/platform"
	"gorm.io/gorm"
)

type record struct {
	OwnerID              string   `gorm:"column:ownerId;primaryKey"`
	ID                   string   `gorm:"column:profileId;primaryKey"`
	Version              int64    `gorm:"column:revision"`
	Config               *profile `gorm:"column:config;serializer:json"`
	EncryptedCredentials *string  `gorm:"column:encryptedCredentials"`
}

func (record) TableName() string { return "synced_ssh_profiles" }

func list(db *gorm.DB, owner string) ([]profile, error) {
	rows := []record{}
	err := db.Select(`"ownerId", "profileId", revision, config`).
		Where(`"ownerId" = ? AND config IS NOT NULL`, owner).Order(`"profileId"`).
		Limit(maxProfiles + 1).Find(&rows).Error
	result := make([]profile, 0, len(rows))
	for _, row := range rows {
		value := *row.Config
		value.Revision = row.Version
		result = append(result, value)
	}
	return result, err
}

type change struct {
	Value                profile
	Delete               bool
	EncryptedCredentials *string
}

// Serialize writes per owner so concurrent creates cannot exceed the limit or lose unrelated records.
func lockOwner(tx *gorm.DB, owner string) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "ssh-profiles:"+owner).Error
}

func mutate(db *gorm.DB, owner string, edit change) ([]profile, error) {
	var result []profile
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := lockOwner(tx, owner); err != nil {
			return err
		}
		row := record{OwnerID: owner, ID: edit.Value.ID}
		err := tx.Where(`"ownerId" = ? AND "profileId" = ?`, owner, row.ID).First(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if row.Version != edit.Value.Revision || (row.Version > 0 && row.Config == nil) {
			return conflict()
		}
		if edit.Delete {
			row.Config = nil
			row.EncryptedCredentials = nil
		} else {
			if row.Version == 0 {
				if err := checkCapacity(tx, owner); err != nil {
					return err
				}
			}
			updateCredentialStorage(&row, edit)
			row.Config = &edit.Value
		}
		row.Version++
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		result, err = list(tx, owner)
		return err
	})
	return result, err
}

func updateCredentialStorage(row *record, edit change) {
	if edit.EncryptedCredentials != nil {
		row.EncryptedCredentials = edit.EncryptedCredentials
		if *edit.EncryptedCredentials == "" {
			row.EncryptedCredentials = nil
		}
		return
	}
	previous := row.Config
	if previous != nil && (previous.Host != edit.Value.Host || previous.Port != edit.Value.Port ||
		previous.Username != edit.Value.Username || previous.Auth != edit.Value.Auth) {
		row.EncryptedCredentials = nil
	}
}

func checkCapacity(tx *gorm.DB, owner string) error {
	var count int64
	if err := tx.Model(&record{}).Where(`"ownerId" = ? AND config IS NOT NULL`, owner).Count(&count).Error; err != nil {
		return err
	}
	if count >= maxProfiles {
		return platform.NewError(409, "client.ssh.profileLimit")
	}
	return nil
}

// Import is insert-only, including tombstones, so retries and old PCs cannot undo edits or deletions.
func migrate(db *gorm.DB, owner string, profiles []profile) ([]profile, error) {
	var result []profile
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := lockOwner(tx, owner); err != nil {
			return err
		}
		for _, value := range profiles {
			var count int64
			if err := tx.Model(&record{}).Where(`"ownerId" = ? AND "profileId" = ?`, owner, value.ID).
				Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				continue
			}
			if err := checkCapacity(tx, owner); err != nil {
				return err
			}
			value.Revision = 0
			if err := tx.Create(&record{OwnerID: owner, ID: value.ID, Version: 1, Config: &value}).Error; err != nil {
				return err
			}
		}
		var err error
		result, err = list(tx, owner)
		return err
	})
	return result, err
}

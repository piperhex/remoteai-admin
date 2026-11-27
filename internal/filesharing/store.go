package filesharing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/piperhex/remoteai-admin/internal/platform"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	chunkBytes = 1 << 20
	maxSize    = 10 << 30
)

var (
	errOffset      = errors.New("unexpected chunk index")
	errChunkSize   = errors.New("unexpected chunk size")
	errFingerprint = errors.New("file fingerprint mismatch")
)

type file struct {
	ID            string    `json:"id" gorm:"primaryKey"`
	Name          string    `json:"name"`
	Size          int64     `json:"size"`
	UploadedBytes int64     `json:"uploadedBytes"`
	Fingerprint   string    `json:"fingerprint"`
	CreatedAt     time.Time `json:"-"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (file) TableName() string { return "shared_files" }

type chunk struct {
	FileID     string `gorm:"primaryKey"`
	ChunkIndex int    `gorm:"primaryKey;autoIncrement:false"`
	Digest     []byte
	Data       []byte
}

func (chunk) TableName() string { return "shared_file_chunks" }

type store struct{ db *gorm.DB }

func (s store) list(ctx context.Context) ([]file, error) {
	items := []file{}
	err := s.db.WithContext(ctx).Order("created_at DESC, id ASC").Find(&items).Error
	return items, err
}

func (s store) get(ctx context.Context, id string) (file, error) {
	var item file
	err := s.db.WithContext(ctx).Take(&item, "id = ?", id).Error
	return item, err
}

func (s store) create(ctx context.Context, item *file, actor *platform.Principal) error {
	item.ID = uuid.NewString()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(item).Error; err != nil {
			return err
		}
		return audit(tx, actor, "file-sharing.create", item.ID)
	})
}

func (s store) append(ctx context.Context, part chunk) (file, error) {
	var item file
	digest := sha256.Sum256(part.Data)
	part.Digest = digest[:]
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&item, "id = ?", part.FileID).Error; err != nil {
			return err
		}
		return saveChunk(tx, &item, part)
	})
	return item, err
}

func saveChunk(tx *gorm.DB, item *file, part chunk) error {
	offset := int64(part.ChunkIndex) * chunkBytes
	if offset >= item.Size || offset > item.UploadedBytes {
		return errOffset
	}
	if int64(len(part.Data)) != min(chunkBytes, item.Size-offset) {
		return errChunkSize
	}
	if offset < item.UploadedBytes {
		// An acknowledgement can be lost after commit. Accept a repeat only for identical bytes.
		var saved chunk
		if err := tx.Select("digest").Take(&saved, "file_id = ? AND chunk_index = ?", part.FileID, part.ChunkIndex).Error; err != nil {
			return err
		}
		if !bytes.Equal(saved.Digest, part.Digest) {
			return errFingerprint
		}
		return nil
	}
	if err := tx.Create(&part).Error; err != nil {
		return err
	}
	item.UploadedBytes += int64(len(part.Data))
	if item.UploadedBytes == item.Size {
		if err := verifyFingerprint(tx, *item); err != nil {
			return err
		}
	}
	return tx.Model(item).Update("uploaded_bytes", item.UploadedBytes).Error
}

func verifyFingerprint(tx *gorm.DB, item file) error {
	var parts []chunk
	if err := tx.Select("digest").Where("file_id = ?", item.ID).Order("chunk_index").Find(&parts).Error; err != nil {
		return err
	}
	combined := make([]byte, 0, len(parts)*sha256.Size)
	for _, part := range parts {
		combined = append(combined, part.Digest...)
	}
	digest := sha256.Sum256(combined)
	if hex.EncodeToString(digest[:]) != item.Fingerprint {
		return errFingerprint
	}
	return nil
}

func (s store) rename(ctx context.Context, item file, actor *platform.Principal) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&item).Update("name", item.Name)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return audit(tx, actor, "file-sharing.rename", item.ID)
	})
}

func (s store) remove(ctx context.Context, ids []string, actor *platform.Principal) (int, error) {
	removed := []file{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).
			Where("id IN ?", ids).Delete(&removed).Error; err != nil {
			return err
		}
		for _, item := range removed {
			if err := audit(tx, actor, "file-sharing.delete", item.ID); err != nil {
				return err
			}
		}
		return nil
	})
	return len(removed), err
}

func audit(tx *gorm.DB, actor *platform.Principal, action, id string) error {
	return tx.Table("admin_audit_logs").Create(map[string]interface{}{
		"id": uuid.NewString(), "actorId": actor.ID, "actorEmail": actor.Email,
		"action": action, "targetType": "shared-file", "targetId": id, "createdAt": time.Now(),
	}).Error
}

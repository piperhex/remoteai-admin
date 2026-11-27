package filesharing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type uploadStatus struct {
	file
	ReceivedChunks []int `json:"receivedChunks,omitempty"`
}

func (s store) status(ctx context.Context, id string) (uploadStatus, error) {
	item, err := s.get(ctx, id)
	state := uploadStatus{file: item}
	if err != nil || item.Size == item.UploadedBytes {
		return state, err
	}
	if err := s.db.WithContext(ctx).Model(&chunk{}).Where("file_id = ?", id).Order("chunk_index").
		Pluck("chunk_index", &state.ReceivedChunks).Error; err != nil {
		return state, err
	}
	var received int64
	for _, index := range state.ReceivedChunks {
		received += min(chunkBytes, item.Size-int64(index)*chunkBytes)
	}
	state.ReceivedBytes = &received
	return state, nil
}

func (s store) saveWindowChunk(ctx context.Context, part chunk) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Shared locks permit independent chunk commits and guard against completion or deletion.
		var item file
		if err := tx.Clauses(clause.Locking{Strength: "KEY SHARE"}).Take(&item, "id = ?", part.FileID).Error; err != nil {
			return err
		}
		if err := checkChunkSize(item, part); err != nil {
			return err
		}
		if item.UploadedBytes == item.Size {
			return matchSavedChunk(tx, part)
		}
		return insertChunk(tx, part)
	})
}

func (s store) complete(ctx context.Context, id string) (file, error) {
	var item file
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&item, "id = ?", id).Error; err != nil {
			return err
		}
		if item.UploadedBytes == item.Size {
			return nil
		}
		if err := verifyFingerprint(tx, item); err != nil {
			return err
		}
		return tx.Model(&item).Update("uploaded_bytes", item.Size).Error
	})
	return item, err
}

func checkChunkSize(item file, part chunk) error {
	offset := int64(part.ChunkIndex) * chunkBytes
	if offset < 0 || offset >= item.Size {
		return errOffset
	}
	if int64(len(part.Data)) != min(chunkBytes, item.Size-offset) {
		return errChunkSize
	}
	return nil
}

func insertChunk(tx *gorm.DB, part chunk) error {
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&part)
	if result.Error != nil || result.RowsAffected != 0 {
		return result.Error
	}
	return matchSavedChunk(tx, part)
}

func matchSavedChunk(tx *gorm.DB, part chunk) error {
	var saved chunk
	err := tx.Select("digest").Take(&saved, "file_id = ? AND chunk_index = ?", part.FileID, part.ChunkIndex).Error
	if err != nil {
		return err
	}
	// A retry whose first acknowledgement was lost must leave the original bytes intact.
	if !bytes.Equal(saved.Digest, part.Digest) {
		return errFingerprint
	}
	return nil
}

func verifyFingerprint(tx *gorm.DB, item file) error {
	var parts []chunk
	if err := tx.Select("chunk_index", "digest").Where("file_id = ?", item.ID).
		Order("chunk_index").Find(&parts).Error; err != nil {
		return err
	}
	if int64(len(parts)) != (item.Size+chunkBytes-1)/chunkBytes {
		return errIncomplete
	}
	combined := make([]byte, 0, len(parts)*sha256.Size)
	for index, part := range parts {
		if part.ChunkIndex != index {
			return errIncomplete
		}
		combined = append(combined, part.Digest...)
	}
	digest := sha256.Sum256(combined)
	if hex.EncodeToString(digest[:]) != item.Fingerprint {
		return errFingerprint
	}
	return nil
}

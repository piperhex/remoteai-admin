package lounge

import (
	"context"

	"github.com/codex-switch/admin-go/internal/platform"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type relationships struct {
	Blocked   []account
	BlockedBy []string
}

type relationshipRepository interface {
	Read(context.Context, string) (relationships, error)
	SetBlock(context.Context, blockChange) (relationships, error)
	Account(context.Context, string) (account, error)
}

type blockChange struct {
	Actor, Target string
	Blocked       bool
}

type postgresRelationships struct{ db *gorm.DB }

func (p postgresRelationships) session(ctx context.Context) *gorm.DB {
	return p.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).WithContext(ctx)
}

func (p postgresRelationships) Read(ctx context.Context, user string) (relationships, error) {
	result := relationships{Blocked: []account{}, BlockedBy: []string{}}
	db := p.session(ctx)
	err := db.Raw(`SELECT u.id, u.email AS account FROM lounge_user_blocks b
		JOIN users u ON u.id = b.blocked_user_id WHERE b.user_id = ? ORDER BY u.id`, user).Scan(&result.Blocked).Error
	if err != nil {
		return result, err
	}
	err = db.Raw(`SELECT user_id FROM lounge_user_blocks WHERE blocked_user_id = ?`, user).Scan(&result.BlockedBy).Error
	return result, err
}

func (p postgresRelationships) Account(ctx context.Context, id string) (account, error) {
	var result account
	query := p.session(ctx).Raw(`SELECT id, email AS account FROM users WHERE id = ? AND disabled = false`, id).
		Scan(&result)
	if query.Error != nil || query.RowsAffected != 1 {
		return result, platform.NewError(404, "client.lounge.userUnavailable")
	}
	return result, nil
}

func (p postgresRelationships) SetBlock(ctx context.Context, change blockChange) (relationships, error) {
	if _, err := p.Account(ctx, change.Target); err != nil {
		return relationships{}, err
	}
	var result relationships
	err := p.session(ctx).Transaction(func(db *gorm.DB) error {
		if err := writeBlock(db, change); err != nil {
			return err
		}
		var err error
		result, err = (postgresRelationships{db: db}).Read(ctx, change.Actor)
		return err
	})
	return result, err
}

func writeBlock(db *gorm.DB, change blockChange) error {
	if change.Blocked {
		return db.Exec(`INSERT INTO lounge_user_blocks(user_id, blocked_user_id) VALUES (?, ?)
			ON CONFLICT DO NOTHING`, change.Actor, change.Target).Error
	}
	return db.Exec(`DELETE FROM lounge_user_blocks WHERE user_id = ? AND blocked_user_id = ?`,
		change.Actor, change.Target).Error
}

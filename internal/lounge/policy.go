package lounge

import (
	"context"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const maxMuteMinutes = 30 * 24 * 60

type policy struct {
	Moderator  bool       `json:"moderator"`
	MutedUntil *time.Time `json:"mutedUntil"`
}

type policyChange struct {
	Moderator *bool `json:"moderator"`
	Minutes   *int  `json:"minutes"`
}

type policyUpdate struct {
	Actor, Target string
	Admin         bool
	Change        policyChange
}

type policyRepository interface {
	Read(context.Context, string) (policy, error)
	Update(context.Context, policyUpdate) (policy, error)
}

type postgresPolicies struct{ db *gorm.DB }

func (p postgresPolicies) session(ctx context.Context) *gorm.DB {
	// This feature persists only current moderation settings, with no SQL or content logging.
	return p.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).WithContext(ctx)
}

func readPolicy(db *gorm.DB, user string) (policy, error) {
	var result policy
	query := db.Raw(`SELECT COALESCE(p.moderator, false) AS moderator, p.muted_until
		FROM users u LEFT JOIN lounge_user_policies p ON p.user_id = u.id WHERE u.id = ? AND u.disabled = false`, user).
		Scan(&result)
	if query.Error != nil {
		return result, platform.NewError(503, "client.lounge.unavailable")
	}
	if query.RowsAffected != 1 {
		return result, platform.NewError(404, "client.lounge.userUnavailable")
	}
	return result, nil
}

func (p postgresPolicies) Read(ctx context.Context, user string) (policy, error) {
	return readPolicy(p.session(ctx), user)
}

func validateChange(request policyUpdate) error {
	change := request.Change
	if change.Moderator == nil && change.Minutes == nil {
		return platform.NewError(400, "client.lounge.invalidDuration")
	}
	if change.Minutes != nil && (*change.Minutes < 0 || *change.Minutes > maxMuteMinutes || request.Actor == request.Target) {
		return platform.NewError(400, "client.lounge.invalidDuration")
	}
	if !request.Admin && change.Moderator != nil {
		return platform.NewError(403, "client.lounge.moderatorOnly")
	}
	return nil
}

func (p postgresPolicies) Update(ctx context.Context, request policyUpdate) (policy, error) {
	var result policy
	if err := validateChange(request); err != nil {
		return result, err
	}
	err := p.session(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize rare moderation writes so a revoked moderator cannot race a mute into a later commit.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(710983120)).Error; err != nil {
			return err
		}
		if !request.Admin {
			actor, err := readPolicy(tx, request.Actor)
			if err != nil || !actor.Moderator {
				return platform.NewError(403, "client.lounge.moderatorOnly")
			}
		}
		var err error
		result, err = readPolicy(tx, request.Target)
		if err != nil {
			return err
		}
		if request.Change.Moderator != nil {
			result.Moderator = *request.Change.Moderator
		}
		if request.Change.Minutes != nil {
			result.MutedUntil = nil
			if *request.Change.Minutes > 0 {
				until := time.Now().UTC().Add(time.Duration(*request.Change.Minutes) * time.Minute)
				result.MutedUntil = &until
			}
		}
		return tx.Exec(`INSERT INTO lounge_user_policies (user_id, moderator, muted_until) VALUES (?, ?, ?)
			ON CONFLICT (user_id) DO UPDATE SET moderator = EXCLUDED.moderator, muted_until = EXCLUDED.muted_until`,
			request.Target, result.Moderator, result.MutedUntil).Error
	})
	return result, err
}

func (h *hub) userPolicy(user string) (policy, error) {
	if h.policies == nil {
		return policy{}, nil
	} // In-memory protocol fixtures do not need a database.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.policies.Read(ctx, user)
}

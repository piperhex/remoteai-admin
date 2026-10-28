package lounge

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type avatarProfile struct {
	ID     string `json:"id"`
	Avatar string `json:"avatar"`
}

type profileRepository interface {
	Read(context.Context, []string) ([]avatarProfile, error)
	Save(context.Context, avatarProfile) error
}

type postgresProfiles struct{ db *gorm.DB }

func (p postgresProfiles) session(ctx context.Context) *gorm.DB {
	return p.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).WithContext(ctx)
}

func (p postgresProfiles) Read(ctx context.Context, ids []string) ([]avatarProfile, error) {
	rows := []avatarProfile{}
	if len(ids) == 0 {
		return rows, nil
	}
	err := p.session(ctx).Raw(`SELECT user_id AS id, avatar FROM lounge_profiles WHERE user_id IN ?`, ids).
		Scan(&rows).Error
	avatars := make(map[string]string, len(rows))
	for _, row := range rows {
		avatars[row.ID] = row.Avatar
	}
	result := make([]avatarProfile, 0, len(ids))
	for _, id := range ids {
		result = append(result, avatarProfile{ID: id, Avatar: avatars[id]})
	}
	return result, err
}

// Only the current profile is retained. There is no message, original upload, or change history.
func (p postgresProfiles) Save(ctx context.Context, profile avatarProfile) error {
	return p.session(ctx).Exec(`INSERT INTO lounge_profiles (user_id, avatar) VALUES (?, ?)
		ON CONFLICT (user_id) DO UPDATE SET avatar = EXCLUDED.avatar`, profile.ID, profile.Avatar).Error
}

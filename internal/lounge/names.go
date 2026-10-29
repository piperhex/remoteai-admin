package lounge

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const maxNicknameLength = 32
const maxRemarkLength = 64

type nicknameProfile struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
}

type contactRemark struct {
	ID     string `json:"id"`
	Remark string `json:"remark"`
}

type nameRepository interface {
	Nicknames(context.Context, []string) ([]nicknameProfile, error)
	Remarks(context.Context, string, []string) ([]contactRemark, error)
	SaveNickname(context.Context, nicknameProfile) error
	SaveRemark(context.Context, string, contactRemark) error
}

type postgresNames struct{ db *gorm.DB }

func (p postgresNames) session(ctx context.Context) *gorm.DB {
	return p.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).WithContext(ctx)
}

func (p postgresNames) Nicknames(ctx context.Context, ids []string) ([]nicknameProfile, error) {
	rows := []nicknameProfile{}
	if len(ids) == 0 {
		return rows, nil
	}
	err := p.session(ctx).Raw(`SELECT user_id AS id, nickname FROM lounge_nicknames WHERE user_id IN ?`, ids).
		Scan(&rows).Error
	values := make(map[string]string, len(rows))
	for _, row := range rows {
		values[row.ID] = row.Nickname
	}
	result := make([]nicknameProfile, 0, len(ids))
	for _, id := range ids {
		result = append(result, nicknameProfile{ID: id, Nickname: values[id]})
	}
	return result, err
}

func (p postgresNames) Remarks(ctx context.Context, owner string, ids []string) ([]contactRemark, error) {
	rows := []contactRemark{}
	if len(ids) == 0 {
		return rows, nil
	}
	err := p.session(ctx).Raw(`SELECT user_id AS id, remark FROM lounge_contact_remarks
		WHERE owner_id = ? AND user_id IN ?`, owner, ids).Scan(&rows).Error
	values := make(map[string]string, len(rows))
	for _, row := range rows {
		values[row.ID] = row.Remark
	}
	result := make([]contactRemark, 0, len(ids))
	for _, id := range ids {
		result = append(result, contactRemark{ID: id, Remark: values[id]})
	}
	return result, err
}

func (p postgresNames) SaveNickname(ctx context.Context, profile nicknameProfile) error {
	return p.session(ctx).Exec(`INSERT INTO lounge_nicknames (user_id, nickname) VALUES (?, ?)
		ON CONFLICT (user_id) DO UPDATE SET nickname = EXCLUDED.nickname`, profile.ID, profile.Nickname).Error
}

func (p postgresNames) SaveRemark(ctx context.Context, owner string, value contactRemark) error {
	if value.Remark == "" {
		return p.session(ctx).Exec(`DELETE FROM lounge_contact_remarks WHERE owner_id = ? AND user_id = ?`,
			owner, value.ID).Error
	}
	return p.session(ctx).Exec(`INSERT INTO lounge_contact_remarks (owner_id, user_id, remark) VALUES (?, ?, ?)
		ON CONFLICT (owner_id, user_id) DO UPDATE SET remark = EXCLUDED.remark`, owner, value.ID, value.Remark).Error
}

func validName(value string, limit int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit &&
		strings.IndexFunc(value, unicode.IsControl) < 0 && !strings.ContainsAny(value, "\u2028\u2029")
}

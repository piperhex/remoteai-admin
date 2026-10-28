package lounge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const awaitingReply = "client.lounge.awaitingReply"

var errAwaitingReply = errors.New(awaitingReply)

type directPermission struct {
	FirstUser, SecondUser string
	Initiator             string
	Fingerprint           string
}

type directPolicyRepository interface {
	Admit(context.Context, directPermission) error
	States(context.Context, string, []string) (map[string]string, error)
}

type postgresDirectPolicy struct{ db *gorm.DB }

func (p postgresDirectPolicy) States(ctx context.Context, user string, peers []string) (map[string]string, error) {
	states := make(map[string]string, len(peers))
	for _, peer := range peers {
		states[peer] = "new"
	}
	var rows []directPermission
	err := p.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).WithContext(ctx).
		Raw(`SELECT first_user, second_user, initiator FROM lounge_direct_permissions
			WHERE (first_user = ? AND second_user IN ?) OR (second_user = ? AND first_user IN ?)`,
			user, peers, user, peers).Scan(&rows).Error
	for _, row := range rows {
		peer := row.FirstUser
		if peer == user {
			peer = row.SecondUser
		}
		states[peer] = "open"
		if row.Initiator == user {
			states[peer] = "waiting"
		}
	}
	return states, err
}

// One current permission per pair, never a message, receipt, timestamp or event history.
// The keyed fingerprint only permits identical retries until the first reply, then is erased.
func (p postgresDirectPolicy) Admit(ctx context.Context, request directPermission) error {
	db := p.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).WithContext(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO lounge_direct_permissions
			(first_user, second_user, initiator, fingerprint) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			request.FirstUser, request.SecondUser, request.Initiator, request.Fingerprint).Error; err != nil {
			return err
		}
		var stored directPermission
		if err := tx.Raw(`SELECT initiator, fingerprint FROM lounge_direct_permissions
			WHERE first_user = ? AND second_user = ? FOR UPDATE`, request.FirstUser, request.SecondUser).
			Scan(&stored).Error; err != nil {
			return err
		}
		if stored.Initiator == "" {
			return nil
		}
		if stored.Initiator == request.Initiator {
			if stored.Fingerprint != request.Fingerprint {
				return errAwaitingReply
			}
			return nil
		}
		return tx.Exec(`UPDATE lounge_direct_permissions SET initiator = NULL, fingerprint = NULL
			WHERE first_user = ? AND second_user = ?`, request.FirstUser, request.SecondUser).Error
	})
}

func (h *hub) directPermission(sender string, input incoming) (directPermission, error) {
	request := directPermission{FirstUser: sender, SecondUser: input.To, Initiator: sender}
	if request.FirstUser > request.SecondUser {
		request.FirstUser, request.SecondUser = request.SecondUser, request.FirstUser
	}
	if input.Images == nil {
		input.Images = []string{}
	}
	// Do not include transport flags or avatar: they may change during an otherwise identical retry.
	data, err := json.Marshal(struct {
		ID, Text string
		Images   []string
		Reply    *replyQuote
	}{input.ID, input.Text, input.Images, input.Reply})
	if err != nil {
		return request, err
	}
	digest := hmac.New(sha256.New, h.directKey)
	digest.Write([]byte("lounge-direct-permission\x00" + sender + "\x00" + input.To + "\x00"))
	digest.Write(data)
	request.Fingerprint = hex.EncodeToString(digest.Sum(nil))
	return request, nil
}

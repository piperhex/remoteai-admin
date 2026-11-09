package accounts

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Bound accounts keep their central identity and metadata. Only a newer OAuth rotation for
// that same identity may replace their credentials; metadata-only permissions never authorize it.
func newerBoundCredentials(current systemAccount, candidate object) bool {
	tokens := obj(candidate["tokens"])
	previousTokens := obj(current.Auth["tokens"])
	if str(previousTokens["access_token"]) == "" || candidate["agent_identity"] != nil {
		return false
	}
	if str(tokens["access_token"]) == "" || str(tokens["refresh_token"]) == "" ||
		(str(tokens["access_token"]) == str(previousTokens["access_token"]) &&
			str(tokens["refresh_token"]) == str(previousTokens["refresh_token"])) {
		return false
	}
	next, err := time.Parse(time.RFC3339Nano, str(candidate["last_refresh"]))
	if err != nil {
		return false
	}
	// Legacy records without a refresh timestamp sort before a validated fresh rotation.
	previous, _ := time.Parse(time.RFC3339Nano, str(current.Auth["last_refresh"]))
	if !next.After(previous) {
		return false
	}
	identity, err := authIdentity(candidate)
	if err != nil || identity["syncAccountId"] != current.SyncAccountID {
		return false
	}
	// When the access token is a JWT, its own identity must agree with the retained ID token.
	if _, readable := parseClaims(str(tokens["access_token"])); readable {
		accessIdentity, err := authIdentity(object{"tokens": object{"access_token": tokens["access_token"]}})
		return err == nil && accessIdentity["syncAccountId"] == current.SyncAccountID
	}
	return true
}

func (s *service) refreshBoundCredentials(options writeOptions, current systemAccount, candidate object) error {
	if !options.RefreshCredentials || !newerBoundCredentials(current, candidate) {
		return nil
	}
	changed, err := s.persistBoundCredentials(current.ID, candidate)
	if err != nil || !changed {
		return err
	}
	if err = s.invalidateSystem(current.ID); err != nil {
		return err
	}
	return s.notifyBoundCredentials(current)
}

func (s *service) persistBoundCredentials(id string, candidate object) (bool, error) {
	changed := false
	err := s.deps.DB.Transaction(func(tx *gorm.DB) error {
		var current systemAccount
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&current).Error; err != nil {
			return err
		}
		if !newerBoundCredentials(current, candidate) {
			return nil
		}
		result := updateBoundCredentialRow(tx, &current, candidate)
		changed = result.RowsAffected > 0
		return result.Error
	})
	return changed, err
}

func updateBoundCredentialRow(tx *gorm.DB, current *systemAccount, candidate object) *gorm.DB {
	return tx.Model(current).Select("Auth", "LastModifiedAt").Updates(&systemAccount{
		Auth: candidate, LastModifiedAt: now(),
	})
}

func (s *service) notifyBoundCredentials(current systemAccount) error {
	if s.deps.AccountAuthChanged == nil {
		return nil
	}
	rows := []binding{}
	if err := s.deps.DB.Where(`"systemAccountId" = ?`, current.ID).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		s.deps.AccountAuthChanged(row.UserID, []string{current.SyncAccountID})
	}
	return nil
}

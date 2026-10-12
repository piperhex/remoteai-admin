package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/codex-switch/admin-go/internal/platform"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const helpSettingsID = "help-assistant"
const maxHelpPromptChars = 16000
const defaultHelpEffort = "low"
const defaultHelpPrompt = "你是 Remote AI 的问题解答助手。使用用户的语言，结合提供的使用说明、当前设置和图片，" +
	"用简洁、友好的步骤回答 Remote AI 相关问题。先给结论，再给操作建议；不确定时说明原因，并提出一个有帮助的问题。"

var helpModelPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]{0,127}$`)

type helpSettings struct {
	Model        string         `json:"model"`
	Effort       string         `json:"effort"`
	SystemPrompt string         `json:"systemPrompt"`
	Revision     string         `json:"revision"`
	Questions    []helpQuestion `json:"questions"`
}

type helpSettingsRow struct {
	ID        string       `gorm:"column:id;primaryKey"`
	Policy    helpSettings `gorm:"column:policy;serializer:json"`
	UpdatedAt time.Time    `gorm:"column:updated_at;autoUpdateTime"`
}

func (helpSettingsRow) TableName() string { return "chat_settings" }

func readHelpSettings(ctx context.Context, db *gorm.DB) (helpSettings, error) {
	row := helpSettingsRow{ID: helpSettingsID, Policy: helpSettings{SystemPrompt: defaultHelpPrompt}}
	err := db.WithContext(ctx).First(&row, "id = ?", helpSettingsID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = nil
	}
	if row.Policy.Effort == "" {
		row.Policy.Effort = defaultHelpEffort
	}
	if row.Policy.Questions == nil {
		row.Policy.Questions = defaultHelpQuestions()
	}
	return row.Policy, err
}

func normalizeHelpSettings(input helpSettings) (helpSettings, error) {
	input.Model, input.SystemPrompt = strings.TrimSpace(input.Model), strings.TrimSpace(input.SystemPrompt)
	input.Effort = strings.TrimSpace(input.Effort)
	if input.Effort == "" {
		input.Effort = defaultHelpEffort
	}
	switch input.Effort {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
	default:
		return input, platform.NewError(400, "请选择有效的推理强度。")
	}
	if !helpModelPattern.MatchString(input.Model) {
		return input, platform.NewError(400, "请选择一个可用的官方模型。")
	}
	if input.SystemPrompt == "" || utf8.RuneCountInString(input.SystemPrompt) > maxHelpPromptChars {
		return input, platform.NewError(400, "请填写系统提示词，最多 16000 个字符。")
	}
	if len(input.Revision) > 128 {
		return input, platform.NewError(400, "请重新加载助手设置后再保存。")
	}
	questions, err := normalizeHelpQuestions(input.Questions)
	input.Questions = questions
	return input, err
}

func validateHelpModel(model string, raw json.RawMessage) error {
	var catalog struct {
		Models []struct {
			Slug       string `json:"slug"`
			ID         string `json:"id"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &catalog) != nil || len(catalog.Models) == 0 {
		return platform.NewError(503, "暂无可用模型，请先更新官方模型列表。")
	}
	for _, candidate := range catalog.Models {
		if candidate.Visibility != "hide" && strings.TrimSpace(first(candidate.Slug, candidate.ID)) == model {
			return nil
		}
	}
	return platform.NewError(400, "该模型不在当前官方模型列表中，请重新选择。")
}

func (s *service) saveHelpSettings(
	ctx context.Context, input helpSettings, actor *platform.Principal,
) (helpSettings, error) {
	previous := input.Revision
	input.Revision = newID()
	err := s.deps.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := helpSettingsRow{ID: helpSettingsID, Policy: helpSettings{SystemPrompt: defaultHelpPrompt}}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", helpSettingsID).Error; err != nil {
			return err
		}
		if row.Policy.Revision != previous {
			return platform.NewError(409, "助手设置已更新，请重新加载后再保存。")
		}
		// Older admin clients omit questions; keep the saved list when they update other settings.
		if input.Questions == nil {
			input.Questions = row.Policy.Questions
			if input.Questions == nil {
				input.Questions = defaultHelpQuestions()
			}
		}
		row.Policy = input
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		audit := &service{deps: &platform.Dependencies{DB: tx}}
		return audit.record(actor, auditOptions{Action: "help-assistant.update", TargetID: helpSettingsID,
			Metadata: object{"model": input.Model, "effort": input.Effort, "revision": input.Revision}})
	})
	return input, err
}

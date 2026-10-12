package accounts

import (
	"strings"
	"unicode/utf8"

	"github.com/codex-switch/admin-go/internal/platform"
)

const maxHelpQuestions = 12
const maxHelpQuestionChars = 200

type helpQuestion struct {
	Zh string `json:"zh"`
	En string `json:"en"`
}

func defaultHelpQuestions() []helpQuestion {
	return []helpQuestion{
		{Zh: "如何选择 Codex 使用的账户？", En: "How do I choose the account Codex uses?"},
		{Zh: "手机为什么连接不上电脑？", En: "Why can’t my phone connect to my computer?"},
		{Zh: "自动切换账户怎么设置？", En: "How do I set up automatic account switching?"},
	}
}

func normalizeHelpQuestions(questions []helpQuestion) ([]helpQuestion, error) {
	if len(questions) > maxHelpQuestions {
		return nil, platform.NewError(400, "常用问题最多添加 12 条。")
	}
	for i, question := range questions {
		question.Zh, question.En = strings.TrimSpace(question.Zh), strings.TrimSpace(question.En)
		if question.Zh == "" && question.En == "" {
			return nil, platform.NewError(400, "请填写常用问题，中文或英文至少填写一项。")
		}
		if utf8.RuneCountInString(question.Zh) > maxHelpQuestionChars ||
			utf8.RuneCountInString(question.En) > maxHelpQuestionChars {
			return nil, platform.NewError(400, "每条常用问题最多 200 个字符。")
		}
		questions[i] = question
	}
	return questions, nil
}

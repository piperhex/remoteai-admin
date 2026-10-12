package accounts

import (
	"strings"
	"testing"
)

func TestHelpQuestionsValidationAndLegacyUpdates(t *testing.T) {
	for _, input := range [][]helpQuestion{nil, {}} {
		result, err := normalizeHelpQuestions(input)
		if err != nil || (result == nil) != (input == nil) {
			t.Fatalf("must distinguish omitted and explicitly empty lists: %#v %v", result, err)
		}
	}
	result, err := normalizeHelpQuestions([]helpQuestion{{Zh: " 如何选择账户？ ", En: " Account? "}, {En: "English only"}})
	if err != nil || result[0].Zh != "如何选择账户？" || result[0].En != "Account?" || result[1].Zh != "" {
		t.Fatal(result, err)
	}
	for _, input := range [][]helpQuestion{
		{{Zh: " "}}, {{Zh: strings.Repeat("界", maxHelpQuestionChars+1)}},
		{{En: strings.Repeat("a", maxHelpQuestionChars+1)}}, make([]helpQuestion, maxHelpQuestions+1),
	} {
		if _, err := normalizeHelpQuestions(input); err == nil {
			t.Fatalf("accepted invalid questions: %#v", input)
		}
	}
	if _, err := normalizeHelpQuestions(defaultHelpQuestions()); err != nil {
		t.Fatal(err)
	}
}

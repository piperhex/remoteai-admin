package content

import "testing"

func TestLoungeSizeSettingsDefaultsAndValidation(t *testing.T) {
	old := defaultChatPolicy()
	delete(old, "titleSettings")
	delete(old, "loungeFileMaxMb")
	delete(old, "loungeGroupImageMaxMb")
	delete(old, "loungeDirectImageMaxMb")
	policy, err := parseChatPolicy(old)
	if err != nil || policy["loungeFileMaxMb"] != float64(300) || policy["loungeGroupImageMaxMb"] != float64(1) ||
		policy["loungeDirectImageMaxMb"] != float64(1) {
		t.Fatal("missing backwards-compatible defaults", policy, err)
	}
	for _, key := range []string{"loungeFileMaxMb", "loungeGroupImageMaxMb", "loungeDirectImageMaxMb"} {
		for _, value := range []float64{0, -1, 1.5} {
			invalid := defaultChatPolicy()
			delete(invalid, "titleSettings")
			invalid[key] = value
			if _, err := parseChatPolicy(invalid); err == nil {
				t.Fatal("accepted invalid size", key, value)
			}
		}
	}
	configured := defaultChatPolicy()
	delete(configured, "titleSettings")
	configured["loungeFileMaxMb"] = float64(500)
	configured["loungeGroupImageMaxMb"] = float64(2)
	configured["loungeDirectImageMaxMb"] = float64(5)
	policy, err = parseChatPolicy(configured)
	if err != nil || policy["loungeFileMaxMb"] != float64(500) || policy["loungeGroupImageMaxMb"] != float64(2) ||
		policy["loungeDirectImageMaxMb"] != float64(5) {
		t.Fatal("configured sizes did not survive parsing", policy, err)
	}
}

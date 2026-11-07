package content

import "testing"

func TestConversationSyncRelayRateIsConfigurableAndBackwardCompatible(t *testing.T) {
	policy := defaultChatPolicy()
	delete(policy, "titleSettings")
	delete(policy, "conversationSyncRelayKbps")
	parsed, err := parseChatPolicy(policy)
	if err != nil || parsed["conversationSyncRelayKbps"] != float64(200) {
		t.Fatalf("missing sync rate must default to 200: %v %v", parsed, err)
	}
	policy["conversationSyncRelayKbps"] = float64(800)
	parsed, err = parseChatPolicy(policy)
	if err != nil || parsed["conversationSyncRelayKbps"] != float64(800) {
		t.Fatalf("configured sync rate was not preserved: %v %v", parsed, err)
	}
	for _, value := range []float64{0, -1, 1.5} {
		policy["conversationSyncRelayKbps"] = value
		if _, err := parseChatPolicy(policy); err == nil {
			t.Fatalf("accepted invalid sync rate %v", value)
		}
	}
}

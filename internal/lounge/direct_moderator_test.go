package lounge

import (
	"errors"
	"testing"
)

func TestModeratorDirectOpeningUsesCurrentRoleWithoutOpeningThePair(t *testing.T) {
	for _, initiallyModerator := range []bool{false, true} {
		t.Run(map[bool]string{false: "promoted", true: "already-moderator"}[initiallyModerator], func(t *testing.T) {
			db := directPolicyDB(t)
			a, b := policyUser(t, db), policyUser(t, db)
			h := newHub(nil)
			h.directKey = []byte("test-key")
			h.directPolicy = postgresDirectPolicy{db: db}
			h.policies = postgresPolicies{db: db}
			setModerator := func(enabled bool) {
				t.Helper()
				_, err := h.policies.Update(t.Context(), policyUpdate{Actor: a, Target: a, Admin: true,
					Change: policyChange{Moderator: &enabled}})
				if err != nil {
					t.Fatal(err)
				}
			}
			setModerator(initiallyModerator)
			for _, id := range []string{"first", "second", "third"} {
				admission, err := h.admitDirect(t.Context(), a, incoming{ID: id, To: b, Text: id})
				if err != nil || admission.Mutual {
					t.Fatal("opening must allow current moderators without creating mutual contacts", admission, err)
				}
				setModerator(true)
			}
			states, err := h.directPolicy.States(t.Context(), a, []string{b})
			if err != nil || states.States[b] != "waiting" {
				t.Fatal("moderator exemption must not permanently open the conversation", states, err)
			}
			setModerator(false)
			if _, err := h.admitDirect(t.Context(), a, incoming{ID: "revoked", To: b, Text: "denied"}); !errors.Is(err, errAwaitingReply) {
				t.Fatal("revoked moderator bypassed opening limit", err)
			}
			admission, err := h.admitDirect(t.Context(), b, incoming{ID: "reply", To: a, Text: "reply"})
			if err != nil || !admission.Mutual {
				t.Fatal("actual reply must create the mutual contact", admission, err)
			}
			if _, err := h.admitDirect(t.Context(), a, incoming{ID: "after-reply", To: b, Text: "allowed"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestModeratorDirectOpeningStillChecksConversationGeneration(t *testing.T) {
	db := directPolicyDB(t)
	a, b := policyUser(t, db), policyUser(t, db)
	h := newHub(nil)
	h.directKey = []byte("test-key")
	h.directPolicy = postgresDirectPolicy{db: db}
	h.policies = &memoryPolicies{values: map[string]policy{a: {Moderator: true}}}
	opening := incoming{ID: "opening", To: b, Text: "hello"}
	if _, err := h.admitDirect(t.Context(), a, opening); err != nil {
		t.Fatal(err)
	}
	pair, err := h.directPermission(a, opening)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := h.directPolicy.Reset(t.Context(), pair)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.admitDirect(t.Context(), a, opening); !errors.Is(err, errDirectReset) {
		t.Fatal("moderator bypassed deletion generation", err)
	}
	opening.Generation = generation
	for _, id := range []string{"new-first", "new-second"} {
		opening.ID = id
		if _, err := h.admitDirect(t.Context(), a, opening); err != nil {
			t.Fatal("moderator could not send in the new conversation", err)
		}
	}
}

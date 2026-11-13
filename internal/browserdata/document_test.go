package browserdata

import (
	"strings"
	"testing"
)

func TestEncryptedVaultValidation(t *testing.T) {
	secret := &Sealed{Nonce: strings.Repeat("ab", 12), Ciphertext: strings.Repeat("cd", 20)}
	salt := strings.Repeat("01", 16)
	doc := Document{Entries: []Entry{{ID: "vault", Kind: "vault", Salt: salt, Check: secret},
		{ID: "password", Kind: "password", Salt: salt, Secret: secret, At: 1}}}
	if err := Validate(doc); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Document){
		func(d *Document) { d.Entries[1].URL = "https://private-site.test" },
		func(d *Document) { d.Entries[1].Title = "plaintext-password" },
		func(d *Document) { d.Entries[1].Salt = strings.Repeat("02", 16) },
		func(d *Document) { d.Entries[1].Secret = &Sealed{Nonce: "short", Ciphertext: "not encrypted"} },
		func(d *Document) { d.Entries = append(d.Entries, d.Entries[1]) },
	} {
		changed := Document{Entries: append([]Entry(nil), doc.Entries...)}
		mutate(&changed)
		if Validate(changed) == nil {
			t.Fatal("accepted invalid vault")
		}
	}
}

func TestVisitValidation(t *testing.T) {
	for _, address := range []string{"javascript:alert(1)", "file:///secret", "https://user:pass@example.test"} {
		if Validate(Document{Entries: []Entry{{ID: "visit", Kind: "visit", URL: address, At: 1}}}) == nil {
			t.Fatal("accepted unsafe visit")
		}
	}
}

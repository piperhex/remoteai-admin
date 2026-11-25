package lounge

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestImageGrantSurvivesProcessRestartAndOldGrantRequiresAuthor(t *testing.T) {
	restarted, url := fixture(t)
	first := newHub([]byte("previous-process-ticket-key"))
	first.directKey = []byte("same-persistent-configuration")
	restarted.directKey = first.directKey
	restarted.relationships = &memoryRelationships{blocks: map[string]map[string]bool{}}
	author, viewer := uuid.NewString(), uuid.NewString()
	file := attachment{ID: uuid.NewString(), Device: uuid.NewString(), Kind: "image", Name: "photo.png", Mime: "image/png", Size: 10}
	input := incoming{To: viewer, Attachments: []attachment{file}}
	if err := first.prepareAttachments(&peer{user: account{ID: author}}, &input); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := restarted.verifyAttachment(input.Attachments[0].Grant, viewer); err != nil {
		t.Fatal("image lost on restart", err)
	}
	oldClaims := historicalImageClaims()
	oldClaims.Subject, oldClaims.Recipient, oldClaims.File, oldClaims.Device = author, viewer, file, file.Device
	if _, _, _, err := restarted.verifyAttachment(signAttachment(t, first, oldClaims), viewer); err == nil {
		t.Fatal("expired process authority was trusted")
	}
	alice := receiptConnection(t, restarted, url, author)
	bob := receiptConnection(t, restarted, url, viewer)
	request := incoming{Type: "image-grant-request", ID: "request", MessageID: "message", To: author,
		FileID: file.ID, Proof: strings.Repeat("ab", 32)}
	write(t, bob, request)
	if received := read(t, alice); received["from"] != viewer || received["fileId"] != file.ID {
		t.Fatal(received)
	}
	write(t, alice, incoming{Type: "image-grant-approve", ID: request.ID, MessageID: request.MessageID,
		FileID: file.ID, To: viewer, Conversation: &viewer, Attachments: []attachment{file}})
	result := read(t, bob)
	token, ok := result["grant"].(string)
	if !ok || result["from"] != author || result["type"] != "image-grant" {
		t.Fatal("missing author-approved grant")
	}
	owner, _, id, err := restarted.verifyAttachment(token, viewer)
	if err != nil || owner != author || id != file.ID {
		t.Fatal("renewal did not preserve the attachment", err)
	}
	if _, _, _, err := restarted.verifyAttachment(token, uuid.NewString()); err == nil {
		t.Fatal("renewal lost its recipient restriction")
	}
}

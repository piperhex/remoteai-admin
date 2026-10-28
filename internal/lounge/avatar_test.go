package lounge

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"strings"
	"testing"
)

func avatarPicture(t *testing.T, size int) string {
	t.Helper()
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewNRGBA(image.Rect(0, 0, size, size))); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(output.Bytes())
}

func TestLoungeAvatarRequiresSmallComplete48PixelPNG(t *testing.T) {
	valid := avatarPicture(t, avatarSize)
	for _, avatar := range []string{"", valid} {
		if validate(incoming{ID: "valid", Text: "message", Avatar: avatar}) != nil {
			t.Fatal("valid avatar rejected")
		}
	}
	for _, avatar := range []string{avatarPicture(t, 49), avatarPicture(t, 32),
		"https://tracking.example/avatar.png", strings.Replace(valid, "image/png", "image/jpeg", 1),
		"data:image/png;base64," + base64.StdEncoding.EncodeToString(make([]byte, maxAvatarBytes+1)),
		valid[:len(valid)-12]} {
		if validate(incoming{ID: "invalid", Text: "message", Avatar: avatar}) == nil {
			t.Fatal("invalid avatar accepted")
		}
	}
}

func TestLoungeForwardsAvatarWithMessageWithoutRetainingProfile(t *testing.T) {
	h, url := fixture(t)
	alice, bob := connect(t, h, url, "alice"), connect(t, h, url, "bob")
	avatar := avatarPicture(t, avatarSize)
	write(t, alice, incoming{Type: "message", ID: "avatar", Text: "hello", Avatar: avatar})
	read(t, alice)
	profile := read(t, bob)["sender"].(map[string]interface{})
	if profile["id"] != "alice" || profile["avatar"] != avatar {
		t.Fatal("avatar missing from authenticated sender")
	}
	write(t, alice, incoming{Type: "message", ID: "default", Text: "default avatar"})
	read(t, alice)
	if _, present := read(t, bob)["sender"].(map[string]interface{})["avatar"]; present {
		t.Fatal("server retained a previous avatar")
	}
}

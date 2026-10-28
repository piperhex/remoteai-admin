package lounge

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"strings"
)

const avatarSize = 48
const maxAvatarBytes = 16 * 1024

// Avatars travel with individual messages; no profile or image is stored on the server.
func validateAvatar(data string) error {
	if data == "" {
		return nil
	}
	payload, ok := strings.CutPrefix(data, "data:image/png;base64,")
	if !ok || len(payload) > base64.StdEncoding.EncodedLen(maxAvatarBytes) {
		return errInvalid
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil || len(decoded) > maxAvatarBytes {
		return errInvalid
	}
	config, err := png.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || config.Width != avatarSize || config.Height != avatarSize {
		return errInvalid
	}
	if _, err := png.Decode(bytes.NewReader(decoded)); err != nil {
		return errInvalid
	}
	return nil
}

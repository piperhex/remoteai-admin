// Package lounge forwards live conversation frames without persistence or logging.
package lounge

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"unicode/utf8"
)

const (
	maxFrameBytes      = 6 * 1024 * 1024
	maxImageBytes      = 1024 * 1024
	maxTotalImageBytes = 3 * 1024 * 1024
	maxImages          = 3
	maxTextLength      = 4000
	maxImagePixels     = 20_000_000
)

type incoming struct {
	Type    string   `json:"type"`
	Ticket  string   `json:"ticket"`
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Images  []string `json:"images"`
	UserID  string   `json:"userId"`
	Minutes *int     `json:"minutes"`
}

type account struct {
	ID      string `json:"id"`
	Account string `json:"account"`
}

type message struct {
	Type   string   `json:"type"`
	ID     string   `json:"id"`
	Sender account  `json:"sender"`
	Text   string   `json:"text"`
	Images []string `json:"images"`
	SentAt string   `json:"sentAt"`
}

var errInvalid = errors.New("invalid lounge frame")

func validate(input incoming) error {
	if len(input.ID) < 1 || len(input.ID) > 80 || utf8.RuneCountInString(input.Text) > maxTextLength ||
		len(input.Images) > maxImages || (strings.TrimSpace(input.Text) == "" && len(input.Images) == 0) {
		return errInvalid
	}
	total := 0
	for _, data := range input.Images {
		size, err := validateImage(data)
		if err != nil {
			return err
		}
		total += size
	}
	if total > maxTotalImageBytes {
		return errInvalid
	}
	return nil
}

func validateImage(data string) (int, error) {
	header, payload, ok := strings.Cut(data, ",")
	formats := map[string]string{"data:image/png;base64": "png", "data:image/jpeg;base64": "jpeg",
		"data:image/gif;base64": "gif"}
	format := formats[header]
	if !ok || format == "" || len(payload) > base64.StdEncoding.EncodedLen(maxImageBytes) {
		return 0, errInvalid
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(payload)
	if err != nil || len(decoded) > maxImageBytes {
		return 0, errInvalid
	}
	config, actual, err := image.DecodeConfig(bytes.NewReader(decoded))
	if err != nil || actual != format || config.Width <= 0 || config.Height <= 0 ||
		int64(config.Width)*int64(config.Height) > maxImagePixels {
		return 0, errInvalid
	}
	return len(decoded), nil
}

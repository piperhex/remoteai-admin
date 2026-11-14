package browserextensions

import (
	"archive/zip"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type manifest struct {
	ManifestVersion int               `json:"manifest_version"`
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Description     string            `json:"description"`
	Locale          string            `json:"default_locale"`
	Icons           map[string]string `json:"icons"`
	Action          struct {
		Icon json.RawMessage `json:"default_icon"`
	} `json:"action"`
	BrowserAction struct {
		Icon json.RawMessage `json:"default_icon"`
	} `json:"browser_action"`
	Permissions []json.RawMessage `json:"permissions"`
	Hosts       []string          `json:"host_permissions"`
}

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.(0|[1-9][0-9]*)){0,3}$`)

func validVersion(version string) bool {
	if len(version) > 23 || !versionPattern.MatchString(version) {
		return false
	}
	nonzero := false
	for _, part := range strings.Split(version, ".") {
		n, err := strconv.Atoi(part)
		if err != nil || n > 65535 {
			return false
		}
		nonzero = nonzero || n > 0
	}
	return nonzero
}

func readManifest(files map[string]*zip.File, root string) (Item, error) {
	data, err := readFile(files[root+"manifest.json"], maxMetadataBytes)
	var value manifest
	if err != nil || json.Unmarshal(data, &value) != nil ||
		(value.ManifestVersion != 2 && value.ManifestVersion != 3) || !validVersion(value.Version) {
		return Item{}, errInvalidArchive
	}
	name := localized(files, root, value.Locale, value.Name)
	description := localized(files, root, value.Locale, value.Description)
	if strings.TrimSpace(name) == "" || len(name) > 512 || len(description) > 4096 {
		return Item{}, errInvalidArchive
	}
	permissions := append([]string{}, value.Hosts...)
	for _, raw := range value.Permissions {
		var permission string
		if json.Unmarshal(raw, &permission) == nil {
			permissions = append(permissions, permission)
		}
	}
	if len(permissions) > 500 {
		return Item{}, errInvalidArchive
	}
	for _, permission := range permissions {
		if len(permission) > 2048 {
			return Item{}, errInvalidArchive
		}
	}
	sort.Strings(permissions)
	icons := iconPaths(value.Action.Icon)
	icons = append(icons, iconPaths(value.BrowserAction.Icon)...)
	icons = append(icons, preferredIcons(value.Icons)...)
	return Item{Name: name, Description: description, Version: value.Version,
		Permissions: permissions, Icon: readIcon(files, root, icons)}, nil
}

func localized(files map[string]*zip.File, root, locale, text string) string {
	if !strings.HasPrefix(text, "__MSG_") || !strings.HasSuffix(text, "__") {
		return text
	}
	if locale == "" {
		locale = "en"
	}
	name := "_locales/" + locale + "/messages.json"
	if !safePath(name) {
		return text
	}
	data, err := readFile(files[root+name], maxMetadataBytes)
	var messages map[string]struct {
		Message string `json:"message"`
	}
	if err != nil || json.Unmarshal(data, &messages) != nil {
		return text
	}
	key := strings.TrimSuffix(strings.TrimPrefix(text, "__MSG_"), "__")
	for candidate, value := range messages {
		if strings.EqualFold(candidate, key) {
			return value.Message
		}
	}
	return text
}

func iconPaths(raw json.RawMessage) []string {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []string{single}
	}
	var icons map[string]string
	if json.Unmarshal(raw, &icons) != nil {
		return nil
	}
	return preferredIcons(icons)
}

func preferredIcons(icons map[string]string) []string {
	paths := []string{}
	for _, size := range []string{"48", "32", "128", "24", "19", "16"} {
		if name := icons[size]; name != "" {
			paths = append(paths, name)
		}
	}
	keys := make([]string, 0, len(icons))
	for key := range icons {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		paths = append(paths, icons[key])
	}
	return paths
}

func readIcon(files map[string]*zip.File, root string, paths []string) string {
	for _, name := range paths {
		name = strings.TrimPrefix(name, "/")
		if !safePath(name) {
			continue
		}
		data, err := readFile(files[root+name], 128<<10)
		if err != nil {
			continue
		}
		mime := http.DetectContentType(data)
		switch mime {
		case "image/png", "image/jpeg", "image/gif", "image/webp", "image/x-icon", "image/bmp":
			return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		}
	}
	return ""
}

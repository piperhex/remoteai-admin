package browserextensions

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"testing"
)

const fixtureManifest = `{"manifest_version":3,"name":"Page marker","version":"1.0","permissions":["storage"],"host_permissions":["https://example.com/*"]}`

func archiveFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, data := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestArchiveMetadataLocalizationIconAndCRX(t *testing.T) {
	manifest := `{"manifest_version":3,"name":"__MSG_title__","description":"__MSG_summary__","default_locale":"en",
		"version":"2.1","action":{"default_icon":{"48":"icon.png"}},"permissions":["storage"]}`
	icon := "\x89PNG\r\n\x1a\n"
	data := archiveFixture(t, map[string]string{"extension/manifest.json": manifest,
		"extension/_locales/en/messages.json": `{"title":{"message":"Page marker"},"summary":{"message":"Mark pages"}}`,
		"extension/icon.png":                  icon})
	pack, err := ReadArchive(data)
	if err != nil || pack.Item.Name != "Page marker" || pack.Item.Description != "Mark pages" ||
		pack.Item.Icon != "data:image/png;base64,iVBORw0KGgo=" || pack.Item.Permissions[0] != "storage" {
		t.Fatal(pack, err)
	}
	for _, version := range []uint32{2, 3} {
		header := make([]byte, 16)
		copy(header, "Cr24")
		binary.LittleEndian.PutUint32(header[4:], version)
		if version == 3 {
			header = header[:12]
		}
		crx, err := ReadArchive(append(header, data...))
		if err != nil || crx.Item.SHA256 != pack.Item.SHA256 || !bytes.Equal(crx.Data, data) {
			t.Fatal(crx, err)
		}
	}
}

func TestArchiveRejectsUnsafePathsAndManifests(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "a\\b", "C:/secret", "NUL.txt", "a/../x", "a./x", "a ", "a:b"} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadArchive(archiveFixture(t, map[string]string{"manifest.json": fixtureManifest, name: "x"}))
			if err == nil {
				t.Fatal("accepted unsafe archive", name)
			}
		})
	}
	for _, files := range []map[string]string{
		{"manifest.json": "{}"}, {"other/manifest.json": fixtureManifest, "outside": "x"},
		{"manifest.json": fixtureManifest, "MANIFEST.json": fixtureManifest},
		{"manifest.json": fixtureManifest, "file": "x", "file/child": "y"},
		{"manifest.json": fixtureManifest, "FILE": "x", "file/child": "y"},
	} {
		if _, err := ReadArchive(archiveFixture(t, files)); err == nil {
			t.Fatal("accepted invalid archive", files)
		}
	}
	for _, version := range []string{"0", "0.0", "1.2.3.4.5", "1.65536", "01.2", "1-beta"} {
		if validVersion(version) {
			t.Fatal("accepted invalid version", version)
		}
	}
}

func TestArchiveRejectsSymlinksBombsAndCRCFailures(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: "manifest.json", Method: zip.Store}
	header.SetMode(os.ModeSymlink | 0777)
	file, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte(fixtureManifest)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadArchive(buffer.Bytes()); err == nil {
		t.Fatal("accepted symlink")
	}
	data := archiveFixture(t, map[string]string{"manifest.json": fixtureManifest})
	central := bytes.Index(data, []byte{'P', 'K', 1, 2})
	binary.LittleEndian.PutUint32(data[central+24:], maxExpandedBytes+1)
	if _, err = ReadArchive(data); err == nil {
		t.Fatal("accepted expanded size bomb")
	}
	data = archiveFixture(t, map[string]string{"manifest.json": fixtureManifest})
	central = bytes.Index(data, []byte{'P', 'K', 1, 2})
	binary.LittleEndian.PutUint32(data[central+16:], 0)
	if _, err = ReadArchive(data); err == nil {
		t.Fatal("accepted CRC mismatch")
	}
}

func TestArchiveExcludesActiveIcons(t *testing.T) {
	var value map[string]interface{}
	if err := json.Unmarshal([]byte(fixtureManifest), &value); err != nil {
		t.Fatal(err)
	}
	value["icons"] = map[string]string{"48": "icon.svg"}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := ReadArchive(archiveFixture(t, map[string]string{"manifest.json": string(data), "icon.svg": "<svg></svg>"}))
	if err != nil || pack.Item.Icon != "" {
		t.Fatal(pack, err)
	}
}

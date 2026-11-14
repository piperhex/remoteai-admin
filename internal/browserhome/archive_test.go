package browserhome

import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func archiveFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, text := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestArchiveAcceptsDistAndRebasesCompletePackage(t *testing.T) {
	data := archiveFixture(t, map[string]string{
		"dist/index.html":      `<html><head><script type="module" src="/assets/app.js"></script><link rel="stylesheet" href="/assets/app.css"></head><body><script>if (1 < 2) window.inlineReady = true;</script></body></html>`,
		"dist/assets/app.js":   `import './chunk.js'; const image = "/logo.svg";`,
		"dist/assets/chunk.js": `window.loaded = true`,
		"dist/assets/app.css":  `body { background: url(/logo.svg); }`,
		"dist/logo.svg":        `<svg xmlns="http://www.w3.org/2000/svg"/>`,
	})
	assets, err := ReadArchive(data)
	if err != nil || len(assets) != 5 {
		t.Fatal(len(assets), err)
	}
	bundle, pack, err := prepareBundle(assets)
	if err != nil {
		t.Fatal(err)
	}
	digest := md5.Sum(pack.Data)
	if hex.EncodeToString(digest[:]) != pack.MD5 {
		t.Fatal("package checksum mismatch")
	}
	prefix := sitePrefix + bundle.Revision + "/"
	for _, asset := range assets {
		if asset.Path == "index.html" && (!strings.Contains(string(asset.Data), `src="`+prefix+`assets/app.js"`) ||
			!strings.Contains(string(asset.Data), "1 < 2") || !strings.Contains(string(asset.Data), `<base href="`+prefix)) {
			t.Fatal(string(asset.Data))
		}
		if asset.Path == "assets/app.js" && (!strings.Contains(string(asset.Data), prefix+"logo.svg") ||
			!strings.Contains(string(asset.Data), "import './chunk.js'")) {
			t.Fatal(string(asset.Data))
		}
	}
	fresh, _ := ReadArchive(data)
	_, again, err := prepareBundle(fresh)
	if err != nil || again.MD5 != pack.MD5 {
		t.Fatal("same upload changed checksum", err)
	}
}

func TestArchiveRejectsUnsafeOrIncompleteUploads(t *testing.T) {
	for _, name := range []string{"../app.js", "/app.js", `C:\app.js`, "a/../app.js", "a//app.js",
		"%2e%2e/app.js", "a.php", "a\n.js"} {
		if _, err := ReadArchive(archiveFixture(t, map[string]string{"index.html": "home", name: "bad"})); err == nil {
			t.Fatalf("accepted unsafe entry %q", name)
		}
	}
	for _, files := range []map[string]string{
		{"app.js": "no home"}, {"dist/index.html": "home", "other/style.css": "body{}"},
		{"index.html": "home", "APP.js": "1", "app.js": "2"},
		{"index.html": strings.Repeat("a", maxFileBytes+1)},
	} {
		if _, err := ReadArchive(archiveFixture(t, files)); err == nil {
			t.Fatal("accepted invalid archive")
		}
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: "index.html"}
	header.SetMode(os.ModeSymlink | 0777)
	file, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadArchive(buffer.Bytes()); err == nil {
		t.Fatal("accepted symlink")
	}
}

package browserbookmarks

import "testing"

func TestBookmarkValidation(t *testing.T) {
	valid := Entry{ID: "guide", Kind: "link", Title: "Guide", URL: "https://example.test"}
	for _, address := range []string{"javascript:alert(1)", "file:///secret", "https://user:password@example.test"} {
		entry := valid
		entry.URL = address
		if Validate(Document{Entries: []Entry{entry}}) == nil {
			t.Fatalf("unsafe URL accepted: %s", address)
		}
	}
	folder := Entry{ID: "folder", Kind: "folder", Title: "Folder"}
	valid.ParentID = folder.ID
	if err := Validate(Document{Entries: []Entry{folder, valid}}); err != nil {
		t.Fatal(err)
	}
	folder.ParentID = folder.ID
	if Validate(Document{Entries: []Entry{folder, valid}}) == nil {
		t.Fatal("folder cycle accepted")
	}
	if Validate(Document{Entries: []Entry{valid}}) == nil {
		t.Fatal("orphan accepted")
	}
	valid.ParentID = ""
	if Validate(Document{Entries: []Entry{valid, valid}}) == nil {
		t.Fatal("duplicate ID accepted")
	}
}

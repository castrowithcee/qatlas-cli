package infomaniakmail

import (
	"encoding/json"
	"strings"
	"testing"
)

func decodeFolders(t *testing.T, raw string) FoldersPage {
	t.Helper()
	var page FoldersPage
	if err := json.Unmarshal([]byte(raw), &page); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return page
}

func folderNames(page FoldersPage) []string {
	names := []string{}
	for _, folder := range page.Folders {
		names = append(names, folder.Name)
	}
	return names
}

func TestFoldersListIsFilteredToTheAllowlist(t *testing.T) {
	e := newEnvironment(t)
	for _, name := range []string{"INBOX", "Allowed", "Secret", "Allowed/Child"} {
		e.f.folder(t, name)
	}

	result, err := e.invoke("infomaniakmail.folders.list", "open", `{}`)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := strings.Join(folderNames(decodeFolders(t, result)), ","); got != "Allowed,Allowed/Child,INBOX,Secret" {
		t.Errorf("open connection folders = %s", got)
	}

	seen := len(e.f.wire.String())
	result, err = e.invoke("infomaniakmail.folders.list", "folders", `{}`)
	if err != nil {
		t.Fatalf("folders: %v", err)
	}
	page := decodeFolders(t, result)
	if got := strings.Join(folderNames(page), ","); got != "Allowed,INBOX" || page.Count != 2 || page.Truncated {
		t.Errorf("restricted connection folders = %s (%+v), want only the allow-list without subfolders", got, page)
	}
	if strings.Contains(result, "Secret") {
		t.Errorf("result %q names a folder outside the allow-list", result)
	}
	// The server was only ever asked for the allow-listed names.
	wire := e.f.wire.String()[seen:]
	if strings.Contains(wire, `LIST "" "*"`) || strings.Contains(wire, "LIST \"\" *") {
		t.Errorf("a restricted connection asked the server for every folder:\n%s", wire)
	}
}

func TestFoldersListWithoutAMatchingFolderIsEmpty(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "Secret")
	result, err := e.invoke("infomaniakmail.folders.list", "allowed", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if page := decodeFolders(t, result); page.Count != 0 || strings.Contains(result, "Secret") {
		t.Errorf("result = %s, want an empty listing", result)
	}
}

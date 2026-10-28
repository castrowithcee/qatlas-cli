package infomaniakmail

import (
	"net"
	"regexp"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

var listDelimiterCommand = regexp.MustCompile(`T\d+ LIST "" ""`)

func treeEnvironment(t *testing.T, targets ...string) *environment {
	t.Helper()
	e := newEnvironmentWith(t, func(cfg *config.Config) {
		c := cfg.Connections["change"]
		c.Targets = append([]string{"mailbox/" + mailbox}, targets...)
		c.Tools = append(c.Tools, foldersList.ID, messagesList.ID, messagesGet.ID)
		cfg.Connections["tree"] = c
	})
	for _, name := range []string{"INBOX", "Kunde", "Kunde/A", "Kunde/A/B", "Kunden", "Kunde2", "Archive", "Trash"} {
		e.f.folder(t, name)
	}
	markSpecialUse(t, e.f, "Trash", imap.MailboxAttrTrash)
	return e
}

func TestTreeTargetValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		targets []string
		wantErr bool
	}{
		{"tree", []string{"folder/Kunde/*"}, false},
		{"tree of a path", []string{"folder/Kunde/Alt/*"}, false},
		{"tree and exact together", []string{"folder/Kunde", "folder/Kunde/*", "folder/Archive"}, false},
		{"all folders", []string{"folder/*"}, true},
		{"empty name", []string{"folder//*"}, true},
		{"wildcard in name", []string{"folder/A*"}, true},
		{"wildcard in middle", []string{"folder/A/*/*"}, true},
		{"percent", []string{"folder/A%/*"}, true},
		{"duplicate", []string{"folder/Kunde/*", "folder/Kunde/*"}, true},
	} {
		_, err := parseScope(append([]string{"mailbox/box@example.com"}, tc.targets...))
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, wantErr %t", tc.name, err, tc.wantErr)
		}
		if err != nil && strings.Contains(err.Error(), "Kunde") {
			t.Errorf("%s: error %q quotes a configured value", tc.name, err)
		}
	}
}

// The meaning of both folder forms, as a target and as an exclusion, with the server's delimiter.
func TestFolderFormMeaning(t *testing.T) {
	names := []string{"A", "A/B", "A/B/C", "B", "AB"}
	for _, tc := range []struct {
		name              string
		targets, excludes []string
		want              []bool // per name
	}{
		{"target folder/A", []string{"folder/A"}, nil, []bool{true, true, true, false, false}},
		{"target folder/A/*", []string{"folder/A/*"}, nil, []bool{false, true, true, false, false}},
		{"exclusion folder/A", nil, []string{"folder/A"}, []bool{false, false, false, true, true}},
		{"exclusion folder/A/*", nil, []string{"folder/A/*"}, []bool{true, false, false, true, true}},
		{"exactly A", []string{"folder/A"}, []string{"folder/A/*"}, []bool{true, false, false, false, false}},
		{"target folder/A/B", []string{"folder/A/B"}, nil, []bool{false, true, true, false, false}},
		{"subfolders of A without A/B", []string{"folder/A/*"}, []string{"folder/A/B"}, []bool{false, false, false, false, false}},
	} {
		bound, err := parseScopeExcluding(append([]string{"mailbox/box@example.com"}, tc.targets...), tc.excludes)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for i, name := range names {
			if got := bound.allowsFolderDelim(name, "/"); got != tc.want[i] {
				t.Errorf("%s: %q = %t, want %t", tc.name, name, got, tc.want[i])
			}
			// The check without network access never refuses what the final check admits.
			if tc.want[i] && !bound.allowsFolder(name) {
				t.Errorf("%s: %q refused before login", tc.name, name)
			}
		}
	}
}

func TestFolderFormsWithOtherDelimiters(t *testing.T) {
	scopeOf := func(targets, excludes []string) scope {
		bound, err := parseScopeExcluding(append([]string{"mailbox/box@example.com"}, targets...), excludes)
		if err != nil {
			t.Fatal(err)
		}
		return bound
	}
	// A flat hierarchy has no subfolders: a folder/PATH is exactly its folder, a folder/PATH/* reaches nothing.
	for _, tc := range []struct {
		bound scope
		name  string
		want  bool
	}{
		{scopeOf([]string{"folder/A"}, nil), "A", true},
		{scopeOf([]string{"folder/A"}, nil), "A/B", false},
		{scopeOf([]string{"folder/A/*"}, nil), "A/B", false},
		{scopeOf(nil, []string{"folder/A"}), "A/B", true},
		{scopeOf(nil, []string{"folder/A/*"}), "A/B", true},
	} {
		if got := tc.bound.allowsFolderDelim(tc.name, ""); got != tc.want {
			t.Errorf("flat %+v: %q = %t, want %t", tc.bound, tc.name, got, tc.want)
		}
	}
	// The delimiter decides: a dot hierarchy does not take a slash name as a subfolder.
	bound := scopeOf([]string{"folder/A"}, nil)
	if bound.allowsFolderDelim("A/B", ".") || !bound.allowsFolderDelim("A.B", ".") || bound.allowsFolderDelim("A.", ".") {
		t.Error("the server's delimiter must decide what a subfolder is")
	}
	// INBOX is case-insensitive as a name. Below it a target compares exactly, an exclusion in any case.
	inbox := scopeOf([]string{"folder/inbox"}, nil)
	for name, want := range map[string]bool{"INBOX": true, "Inbox": true, "INBOX/Sub": true, "Inbox/Sub": false} {
		if got := inbox.allowsFolderDelim(name, "/"); got != want {
			t.Errorf("target INBOX: %q = %t, want %t", name, got, want)
		}
	}
	excluded := scopeOf(nil, []string{"folder/INBOX"})
	for _, name := range []string{"INBOX", "inbox", "INBOX/Sub", "inbox/Sub"} {
		if excluded.allowsFolderDelim(name, "/") {
			t.Errorf("exclusion INBOX: %q admitted", name)
		}
	}
}

func TestTreeListsTheFolderAndSubfoldersOnly(t *testing.T) {
	e := treeEnvironment(t, "folder/Kunde")
	e.f.add(t, "Kunde", "a@example.net", "root", base)
	e.f.add(t, "Kunde/A/B", "a@example.net", "deep", base)
	e.f.add(t, "Kunden", "a@example.net", "sibling", base)
	e.f.add(t, "Kunde2", "a@example.net", "sibling2", base)
	before := len(e.f.wire.String())

	for _, folder := range []string{"Kunde", "Kunde/A/B"} {
		if page := list(t, e, "tree", `{"folder":"`+folder+`"}`); page.Folder != folder || len(page.Messages) != 1 {
			t.Errorf("%s: page = %+v", folder, page)
		}
	}
	for _, folder := range []string{"Kunden", "Kunde2", "Archive", "kunde/A"} {
		_, err := e.invoke("infomaniak.mail.messages.list", "tree", `{"folder":"`+folder+`"}`)
		if !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", folder, err)
		} else if strings.Contains(err.Error(), "Kunde") {
			t.Errorf("%s: refusal %q names a folder", folder, err)
		}
	}
	// Siblings that begin with NAME were refused after the delimiter was known and never opened.
	wire := e.f.wire.String()[before:]
	if strings.Contains(wire, "Kunden") || strings.Contains(wire, "Kunde2") {
		t.Errorf("a sibling folder reached the server:\n%s", wire)
	}
	// Reading the exact folder asks nothing; each subfolder call asks once.
	if n := len(listDelimiterCommand.FindAllString(wire, -1)); n != 3 {
		t.Errorf("LIST \"\" \"\" sent %d times, want once per call on a subfolder or prefix sibling (3)", n)
	}

	_, err := e.invoke("infomaniak.mail.messages.get", "tree", `{"folder":"Kunden","uid":1,"uidvalidity":1}`)
	if !isInvalidRequest(err) {
		t.Errorf("get on a sibling: err = %v", err)
	}

	// folder/PATH/* reaches the subfolders without the folder itself.
	e = treeEnvironment(t, "folder/Kunde/*")
	e.f.add(t, "Kunde/A", "a@example.net", "child", base)
	if page := list(t, e, "tree", `{"folder":"Kunde/A"}`); len(page.Messages) != 1 {
		t.Errorf("subfolder: page = %+v", page)
	}
	if _, err := e.invoke("infomaniak.mail.messages.list", "tree", `{"folder":"Kunde"}`); !isInvalidRequest(err) {
		t.Errorf("the folder of a folder/PATH/* target: err = %v, want an invalid request", err)
	}
}

func TestTreeAskOnlyWhenNeeded(t *testing.T) {
	asked := func(e *environment) int { return len(listDelimiterCommand.FindAllString(e.f.wire.String(), -1)) }

	// No folder target and no folder exclusion: never a LIST for the delimiter.
	e := treeEnvironment(t)
	e.f.add(t, "Kunde/A", "a@example.net", "x", base)
	list(t, e, "tree", `{"folder":"Kunde"}`)
	list(t, e, "tree", `{"folder":"Kunde/A"}`)
	if n := asked(e); n != 0 {
		t.Errorf("LIST \"\" \"\" sent %d times without a folder target or exclusion", n)
	}

	// With a folder target: none for the exact folder or a name it does not begin, one per call for a subfolder.
	e = treeEnvironment(t, "folder/Kunde")
	e.f.add(t, "Kunde", "a@example.net", "x", base)
	e.f.add(t, "Kunde/A", "a@example.net", "y", base)
	list(t, e, "tree", `{"folder":"Kunde"}`)
	if _, err := e.invoke("infomaniak.mail.messages.list", "tree", `{"folder":"Archive"}`); !isInvalidRequest(err) {
		t.Errorf("a folder outside the target: %v", err)
	}
	if n := asked(e); n != 0 {
		t.Errorf("exact folder or foreign name asked %d times, want none", n)
	}
	list(t, e, "tree", `{"folder":"Kunde/A"}`)
	if n := asked(e); n != 1 {
		t.Errorf("subfolder asked %d times, want 1", n)
	}

	// Exactly one folder: the folder itself asks nothing, a subfolder is asked about and refused.
	e = excludeEnvironment(t, []string{"folder/Kunde"}, []string{"folder/Kunde/*"})
	e.f.add(t, "Kunde", "a@example.net", "x", base)
	list(t, e, "ex", `{"folder":"Kunde"}`)
	if n := asked(e); n != 0 {
		t.Errorf("the exact folder asked %d times, want none", n)
	}
	if _, err := e.invoke("infomaniak.mail.messages.list", "ex", `{"folder":"Kunde/A"}`); !isInvalidRequest(err) {
		t.Errorf("a subfolder of exactly one folder: err = %v, want an invalid request", err)
	}
	if n := asked(e); n != 1 {
		t.Errorf("subfolder asked %d times, want 1", n)
	}
}

func TestTreeMoveChecksSourceAndDestination(t *testing.T) {
	e := treeEnvironment(t, "folder/Kunde")
	src := e.f.add(t, "Kunde/A", "a@example.net", "x", base)
	validity := validityOf(t, e, "Kunde/A")

	for _, destination := range []string{"Archive", "Kunden", "Kunde2"} {
		_, err := e.confirmed("infomaniak.mail.messages.move", "tree", refArgs("Kunde/A", src, validity, `,"destination":"`+destination+`"`))
		if !isInvalidRequest(err) {
			t.Errorf("destination %s: err = %v, want an invalid request", destination, err)
		}
	}
	if commands(e, moveCommand) != 0 {
		t.Errorf("a refused destination reached MOVE:\n%s", e.f.wire.String())
	}
	if _, err := e.confirmed("infomaniak.mail.messages.move", "tree", refArgs("Kunde/A", src, validity, `,"destination":"Kunde/A/B"`)); err != nil {
		t.Fatalf("move into a subfolder: %v", err)
	}
	if n := len(listDelimiterCommand.FindAllString(e.f.wire.String(), -1)); n < 1 {
		t.Error("move into a subfolder never asked for the delimiter")
	}
	if commands(e, moveCommand) != 1 {
		t.Errorf("want one MOVE:\n%s", e.f.wire.String())
	}
}

func TestTreeTrashMustBeInsideTheScope(t *testing.T) {
	e := treeEnvironment(t, "folder/Kunde")
	uid := e.f.add(t, "Kunde/A", "a@example.net", "x", base)
	validity := validityOf(t, e, "Kunde/A")
	_, err := e.confirmed("infomaniak.mail.messages.delete", "tree", refArgs("Kunde/A", uid, validity, ""))
	if !isInvalidRequest(err) {
		t.Errorf("trash outside the tree: err = %v, want a refusal", err)
	}

	e = treeEnvironment(t, "folder/Kunde")
	e.f.folder(t, "Kunde/Trash")
	markSpecialUse(t, e.f, "Kunde/Trash", imap.MailboxAttrTrash)
	markSpecialUse(t, e.f, "Trash", imap.MailboxAttr("\\Junk"))
	uid = e.f.add(t, "Kunde/A", "a@example.net", "x", base)
	validity = validityOf(t, e, "Kunde/A")
	if _, err := e.confirmed("infomaniak.mail.messages.delete", "tree", refArgs("Kunde/A", uid, validity, "")); err != nil {
		t.Errorf("trash inside the tree: %v", err)
	}
	if n := len(listDelimiterCommand.FindAllString(e.f.wire.String(), -1)); n != 0 {
		t.Errorf("a delimiter from the special-use LIST was asked again (%d)", n)
	}
}

func TestTreeFoldersListFilter(t *testing.T) {
	for _, tc := range []struct {
		targets, excludes []string
		want              string
	}{
		{[]string{"folder/Kunde", "folder/Archive"}, nil, "Archive,Kunde,Kunde/A,Kunde/A/B"},
		{[]string{"folder/Kunde/*"}, nil, "Kunde/A,Kunde/A/B"},
		{[]string{"folder/Kunde"}, []string{"folder/Kunde/*"}, "Kunde"},
	} {
		e := excludeEnvironment(t, tc.targets, tc.excludes)
		result, err := e.invoke("infomaniak.mail.folders.list", "ex", `{}`)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(folderNames(decodeFolders(t, result)), ","); got != tc.want {
			t.Errorf("%v without %v: folders = %s, want %s", tc.targets, tc.excludes, got, tc.want)
		}
		if n := len(listDelimiterCommand.FindAllString(e.f.wire.String(), -1)); n != 0 {
			t.Errorf("folders.list asked for the delimiter separately (%d)", n)
		}
	}
}

func TestFolderOKFailsClosed(t *testing.T) {
	bound, _ := parseScope([]string{"mailbox/box@example.com", "folder/Kunde"})
	// A server without a delimiter admits only the target itself, with no further question.
	flat := &session{scope: bound, delimKnown: true}
	if err := flat.folderOK("op", "Kunde"); err != nil {
		t.Errorf("target: %v", err)
	}
	if err := flat.folderOK("op", "Kunde/A"); !isInvalidRequest(err) {
		t.Errorf("flat hierarchy subfolder: %v, want a refusal", err)
	}
	// A failing question refuses, and the error does not name the folder.
	server, client := net.Pipe()
	_ = server.Close()
	broken := &session{scope: bound, client: imapclient.New(client, nil)}
	err := broken.folderOK("op", "KundeSecret/A")
	if err == nil || strings.Contains(err.Error(), "KundeSecret") {
		t.Errorf("failed LIST: err = %v, want a refusal without the folder name", err)
	}
}

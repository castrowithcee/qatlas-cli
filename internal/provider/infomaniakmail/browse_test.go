package infomaniakmail

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func browseRequest(e *environment, targets []string, req config.TargetListRequest) (config.TargetPage, error) {
	resolved := &config.Resolved{
		Name: "browse", Provider: Provider, Credential: "mail-reader",
		Secrets: coreConfig().Credentials["mail-reader"], Targets: targets,
	}
	secrets := secret.NewWith(func(name string) string {
		if name == passwordEnv {
			return e.f.password.Load().(string)
		}
		return ""
	}, nil, nil, e.red)
	return ListTargets(context.Background(), resolved, secrets, e.red, req)
}

func targetsOf(page config.TargetPage) []string {
	out := []string{}
	for _, entry := range page.Entries {
		out = append(out, entry.Target)
	}
	return out
}

func TestBrowseRegistersTwoLevels(t *testing.T) {
	reg := registry(t)
	if !reg.HasTargetBrowser(Provider) {
		t.Fatal("no target browser")
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	folder, _ := metadata.Target.KindByName("folder")
	sender, _ := metadata.Target.KindByName("sender")
	if folder.Parent != "mailbox" || sender.Parent != "" || !metadata.Target.HasChildKind("mailbox") {
		t.Errorf("kinds = %+v", metadata.Target.Kinds)
	}
	if !metadata.Target.ValidTarget("folder", "folder/Kunden/*") || !metadata.Target.ValidTarget("folder", "folder/Kunden") {
		t.Error("folder forms are not valid targets of the folder kind")
	}
}

func TestBrowseMailboxLevelOffersOnlyTheConfiguredMailboxWithoutALogin(t *testing.T) {
	e := newEnvironment(t)
	page, err := browseRequest(e, []string{"mailbox/" + mailbox, "folder/INBOX"}, config.TargetListRequest{Kind: "mailbox"})
	if err != nil {
		t.Fatal(err)
	}
	want := []config.TargetEntry{{Target: "mailbox/" + mailbox, Name: mailbox, Kind: "mailbox", HasChildren: true}}
	if !reflect.DeepEqual(page.Entries, want) || page.Next != "" {
		t.Errorf("page = %+v, want %+v", page, want)
	}
	for _, targets := range [][]string{nil, {"folder/INBOX"}, {"mailbox/a@example.com", "mailbox/b@example.com"}} {
		page, err := browseRequest(e, targets, config.TargetListRequest{Kind: "mailbox"})
		if err != nil || len(page.Entries) != 0 {
			t.Errorf("targets %q: page = %+v, err = %v, want an empty page", targets, page, err)
		}
	}
	if _, err := browseRequest(e, []string{"mailbox/" + mailbox}, config.TargetListRequest{Kind: "mailbox", Cursor: "2"}); !isInvalidRequest(err) {
		t.Errorf("cursor on the mailbox level: err = %v", err)
	}
	if e.f.dials.Load() != 0 || e.f.reads.Load() != 0 {
		t.Errorf("the mailbox level dialled %d times and read %d secrets", e.f.dials.Load(), e.f.reads.Load())
	}
}

func TestBrowseFolderLevelListsEveryFolderWithSubfolderEntries(t *testing.T) {
	e := newEnvironment(t)
	for _, name := range []string{"INBOX", "Kunden", "Kunden/A", "Kunden/A/2026", "Secret", "Flat"} {
		e.f.folder(t, name)
	}
	// The connection's folder and sender targets do not filter the choices.
	targets := []string{"mailbox/" + mailbox, "folder/Flat", "sender/alice@example.net"}
	page, err := browseRequest(e, targets, config.TargetListRequest{Kind: "folder", Parent: "mailbox/" + mailbox})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"folder/Flat", "folder/INBOX", "folder/Kunden", "folder/Kunden/*", "folder/Kunden/A",
		"folder/Kunden/A/*", "folder/Kunden/A/2026", "folder/Secret"}
	if got := targetsOf(page); !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %q, want %q", got, want)
	}
	byTarget := map[string]config.TargetEntry{}
	for _, entry := range page.Entries {
		byTarget[entry.Target] = entry
	}
	if got := byTarget["folder/Kunden/A/2026"]; got.Name != "2026" || got.Path != "/Kunden/A" || got.Kind != "folder" {
		t.Errorf("subfolder entry = %+v", got)
	}
	if got := byTarget["folder/Kunden/A/*"]; got.Name != "A (with subfolders)" || got.Path != "/Kunden" {
		t.Errorf("tree entry = %+v", got)
	}
	if got := byTarget["folder/INBOX"]; got.Name != "INBOX" || got.Path != "/" {
		t.Errorf("top-level entry = %+v", got)
	}
	if strings.Contains(e.f.wire.String(), `LIST "" "Flat"`) {
		t.Error("the choice asked for single names instead of the whole list")
	}
}

func TestBrowseFolderLevelPages(t *testing.T) {
	e := newEnvironment(t)
	for i := 0; i < 60; i++ {
		e.f.folder(t, fmt.Sprintf("F%03d", i))
	}
	req := config.TargetListRequest{Kind: "folder", Parent: "mailbox/" + mailbox}
	first, err := browseRequest(e, []string{"mailbox/" + mailbox}, req)
	if err != nil || len(first.Entries) != browsePageSize || first.Next != "2" {
		t.Fatalf("first page: %d entries, next %q, err %v", len(first.Entries), first.Next, err)
	}
	req.Cursor = first.Next
	second, err := browseRequest(e, []string{"mailbox/" + mailbox}, req)
	if err != nil || len(second.Entries) != 10 || second.Next != "" || second.Entries[0].Target != "folder/F050" {
		t.Fatalf("second page: %v, next %q, err %v", targetsOf(second), second.Next, err)
	}
	for _, cursor := range []string{"0", "-1", "x", "02", "100001"} {
		req.Cursor = cursor
		if _, err := browseRequest(e, []string{"mailbox/" + mailbox}, req); !isInvalidRequest(err) {
			t.Errorf("cursor %q: err = %v, want invalid request", cursor, err)
		}
	}
}

func TestBrowseRefusesAForeignParentBeforeAnyLogin(t *testing.T) {
	e := newEnvironment(t)
	e.f.folder(t, "INBOX")
	targets := []string{"mailbox/" + mailbox}
	for _, parent := range []string{"", "mailbox/other@example.com", "mailbox/" + strings.ToUpper(mailbox), "folder/INBOX",
		"sender/" + mailbox, "mailbox/not-an-address"} {
		_, err := browseRequest(e, targets, config.TargetListRequest{Kind: "folder", Parent: parent})
		if !isInvalidRequest(err) {
			t.Errorf("parent %q: err = %v, want invalid request", parent, err)
		} else if parent != "" && strings.Contains(err.Error(), strings.TrimPrefix(parent, "mailbox/")) {
			t.Errorf("parent %q: error %q quotes the value", parent, err)
		}
	}
	// Without a mailbox target there is no address to log in with.
	if _, err := browseRequest(e, nil, config.TargetListRequest{Kind: "folder", Parent: "mailbox/" + mailbox}); !isInvalidRequest(err) {
		t.Errorf("no mailbox target: err = %v", err)
	}
	for _, kind := range []string{"sender", "label", ""} {
		if _, err := browseRequest(e, targets, config.TargetListRequest{Kind: kind}); !isInvalidRequest(err) {
			t.Errorf("kind %q: err = %v", kind, err)
		}
	}
	if e.f.dials.Load() != 0 || e.f.reads.Load() != 0 {
		t.Errorf("refused requests dialled %d times and read %d secrets", e.f.dials.Load(), e.f.reads.Load())
	}
}

func TestFolderEntriesDelimiters(t *testing.T) {
	folders := []Folder{
		{Name: "INBOX", Delimiter: "."},
		{Name: "Kunden", Delimiter: "."},
		{Name: "Kunden.A", Delimiter: "."},
		{Name: "Solo", Delimiter: ".", Attributes: []string{`\HasNoChildren`}},
		{Name: "Marked", Delimiter: ".", Attributes: []string{`\HasChildren`}},
		{Name: "Dotted.Name"},
		{Name: "Kunde"},
		{Name: "Kunde2", Delimiter: "."},
		{Name: "bad*name", Delimiter: "."},
	}
	got := map[string]config.TargetEntry{}
	var order []string
	for _, entry := range folderEntries(folders) {
		got[entry.Target] = entry
		order = append(order, entry.Target)
	}
	wantOrder := []string{"folder/INBOX", "folder/Kunden", "folder/Kunden/*", "folder/Kunden.A", "folder/Solo",
		"folder/Marked", "folder/Marked/*", "folder/Dotted.Name", "folder/Kunde", "folder/Kunde2"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("targets = %q, want %q", order, wantOrder)
	}
	if e := got["folder/Kunden.A"]; e.Name != "A" || e.Path != "/Kunden" {
		t.Errorf("dot delimiter entry = %+v", e)
	}
	// A flat hierarchy (NIL delimiter) keeps the whole name and offers no subfolders, even for a dotted name.
	if e := got["folder/Dotted.Name"]; e.Name != "Dotted.Name" || e.Path != "/" {
		t.Errorf("NIL delimiter entry = %+v", e)
	}
	if _, ok := got["folder/Kunde/*"]; ok {
		t.Error("a folder without a delimiter got a subfolder entry")
	}
	if e := got["folder/Kunden/*"]; e.Name != "Kunden (with subfolders)" {
		t.Errorf("tree entry = %+v", e)
	}
	// The slash is no delimiter of its own when the server uses another one.
	slashed := folderEntries([]Folder{{Name: "a/b", Delimiter: "."}})
	if len(slashed) != 1 || slashed[0].Name != "a/b" || slashed[0].Path != "/" {
		t.Errorf("slash under a dot delimiter = %+v", slashed)
	}
}

func TestBrowseLeavesExistingConfigsUnchanged(t *testing.T) {
	cfg := coreConfig()
	before := fmt.Sprintf("%+v", cfg.Connections)
	e := newEnvironmentWith(t, func(c *config.Config) { c.Connections = cfg.Connections })
	e.f.folder(t, "INBOX")
	if _, err := browseRequest(e, cfg.Connections["folders"].Targets,
		config.TargetListRequest{Kind: "folder", Parent: "mailbox/" + mailbox}); err != nil {
		t.Fatal(err)
	}
	if after := fmt.Sprintf("%+v", cfg.Connections); after != before {
		t.Error("browsing changed the connections")
	}
	for name, c := range coreConfig().Connections {
		if err := validateTargets(c.Targets); err != nil {
			t.Errorf("connection %s no longer validates: %v", name, err)
		}
	}
}

func validateTargets(targets []string) error { _, err := parseScope(targets); return err }

// Every entry of the folder level must pass the core's form check, whatever the hierarchy delimiter.
func TestFolderEntriesPassTheCoreFormCheck(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, delim := range []string{"/", "."} {
		folders := []Folder{
			{Name: "INBOX", Delimiter: delim},
			{Name: "Kunden", Delimiter: delim},
			{Name: "Kunden" + delim + "A", Delimiter: delim},
			{Name: "Kunden" + delim + "A" + delim + "2026", Delimiter: delim},
		}
		entries := folderEntries(folders)
		if len(entries) != 6 {
			t.Fatalf("delimiter %q: %d entries, want 6", delim, len(entries))
		}
		for _, entry := range entries {
			if !metadata.Target.ValidTarget("folder", entry.Target) {
				t.Errorf("delimiter %q: %q is no valid folder target", delim, entry.Target)
			}
		}
	}
	for _, bad := range []string{"folder/*", "folder//*", "folder/a/*/*"} {
		if metadata.Target.ValidTarget("folder", bad) {
			t.Errorf("%q must not be a valid folder target", bad)
		}
	}
}

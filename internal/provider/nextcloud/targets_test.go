package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func listed(values ...string) *config.Resolved {
	r := resolvedConnection("x", "cloud-reader", aliceUserEnv, aliceTokenEnv, mainInstance, "")
	r.Targets = values
	return r
}

func TestLegacySingleTargetsKeepTheirMeaning(t *testing.T) {
	for target, want := range map[string]string{
		"Reports": "Reports", "Team/Reports": "Team/Reports", "/": "", "Calendar/2026": "Calendar/2026",
	} {
		s, err := scopeOf(resolvedConnection("x", "c", "u", "p", mainInstance, target))
		if err != nil || !s.hasFolder || strings.Join(s.folder, "/") != want {
			t.Errorf("scopeOf(%q) = %+v, %v", target, s, err)
		}
		if err := validateSingleTarget(target); err != nil {
			t.Errorf("validateSingleTarget(%q) = %v", target, err)
		}
	}
}

func TestLegacySingleTargetWithAKindNameIsRefusedWithoutQuotingIt(t *testing.T) {
	for _, target := range []string{"calendar/2026", "talk", "folder/Reports", "folder", "/deck/1", "admin"} {
		err := validateSingleTarget(target)
		if err == nil || !strings.Contains(err.Error(), "folder/PATH") || strings.ContainsAny(err.Error(), "0123456789") {
			t.Errorf("validateSingleTarget(%q) = %v, want a hint to folder/PATH", target, err)
		}
		if _, err := scopeOf(resolvedConnection("x", "c", "u", "p", mainInstance, target)); err == nil {
			t.Errorf("scopeOf(%q) accepted a legacy value with a kind name", target)
		}
	}
}

func TestTypedTargetLists(t *testing.T) {
	s, err := parseScope([]string{"folder/Team/Reports", "calendar/2026", "calendar/work", "addressbook",
		"talk/abc123", "deck/42", "notes/Work/Plans", "account"})
	if err != nil {
		t.Fatalf("parseScope = %v", err)
	}
	if !s.hasFolder || strings.Join(s.folder, "/") != "Team/Reports" || len(s.calendars.ids) != 2 ||
		!s.addressbooks.all || s.talks.ids[0] != "abc123" || s.decks.ids[0] != "42" ||
		s.notes.ids[0] != "Work/Plans" || !s.account || s.admin {
		t.Errorf("scope = %+v", s)
	}
	for _, values := range [][]string{{"folder"}, {"folder/Reports"}, {"Reports"}, {"/"}, {"admin"}, {"calendar", "account"}} {
		if _, err := parseScope(values); err != nil {
			t.Errorf("parseScope(%v) = %v", values, err)
		}
	}
	whole, _ := parseScope([]string{"folder"})
	if !whole.hasFolder || len(whole.folder) != 0 {
		t.Errorf("folder alone = %+v, want the whole Files root", whole)
	}
}

func TestInvalidTargetSetsAreRefusedWithoutQuotingValues(t *testing.T) {
	many := make([]string, 0, 101)
	for i := 0; i < 101; i++ {
		many = append(many, "calendar/c"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	tests := map[string][]string{
		"two folders":              {"folder/A", "folder/B"},
		"folder and legacy":        {"folder/A", "B"},
		"general and specific":     {"talk", "talk/SECRETTOKEN"},
		"admin with another":       {"admin", "calendar"},
		"admin twice":              {"admin", "admin"},
		"duplicate":                {"calendar/SECRETID", "calendar/SECRETID"},
		"too many":                 many,
		"slash in id":              {"calendar/a/SECRETID"},
		"percent in id":            {"talk/SECRET%2f"},
		"backslash in id":          {`addressbook/SECRET\x`},
		"control in id":            {"calendar/SECRET\x01"},
		"empty id":                 {"talk/"},
		"empty entry":              {" "},
		"board not numeric":        {"deck/SECRETX"},
		"board empty":              {"deck/"},
		"account with id":          {"account/SECRET"},
		"traversing category":      {"notes/../SECRET"},
		"empty category":           {"notes/"},
		"traversing folder":        {"folder/../SECRET"},
		"folder with empty path":   {"folder/"},
		"folder with absolute":     {"folder//SECRET"},
		"legacy traversal in list": {"Reports/../SECRET"},
	}
	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateTargetSet(values)
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Errorf("error quotes a value: %v", err)
			}
		})
	}
	if err := validateTarget("deck/SECRETX"); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("validateTarget = %v", err)
	}
}

func TestFilesToolsRefuseWithoutAFolderBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	for _, targets := range [][]string{{"calendar/SECRETID"}, {"account"}, {"admin"}} {
		for name, h := range map[string]func() (any, error){
			"list": func() (any, error) {
				return folderBound(invokeFilesList)(context.Background(), listed(targets...), res, &redact.Redactor{}, json.RawMessage(`{}`))
			},
			"create": func() (any, error) {
				return folderBound(invokeFilesCreate)(context.Background(), listed(targets...), res, &redact.Redactor{},
					json.RawMessage(`{"path":"a.txt","local_path":"/nonexistent"}`))
			},
			"open": func() (any, error) {
				return Open(context.Background(), listed(targets...), res, &redact.Redactor{})
			},
		} {
			_, err := h()
			if err == nil || classOf(err) != provider.ClassPermission {
				t.Errorf("%s %v = %v, want a permission refusal", name, targets, err)
			}
			if err != nil && (strings.Contains(err.Error(), "SECRETID") || strings.Contains(err.Error(), "calendar")) {
				t.Errorf("refusal names the foreign target: %v", err)
			}
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func TestOpenBindsTheFolderOfATypedList(t *testing.T) {
	red := &redact.Redactor{}
	c, err := Open(context.Background(), listed("calendar", "folder/Team/Reports"), resolver(red), red)
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	if got := c.requestURL([]string{"q1.pdf"}); got != mainInstance+"/remote.php/dav/files/"+aliceUser+"/Team/Reports/q1.pdf" {
		t.Errorf("URL = %q", got)
	}
}

func TestConnectionTestWithoutAFolderReadsTheFilesRoot(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(folderXML(r.URL.EscapedPath(), "alice", "1", "1"))), nil
	})
	red := &redact.Redactor{}
	class, err := TestConnection(context.Background(), listed("deck", "account"), resolver(red), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("TestConnection = %q, %v", class, err)
	}
	if len(*calls) != 1 || (*calls)[0].method != "PROPFIND" || (*calls)[0].depth != depthSelf ||
		(*calls)[0].url.Path != "/remote.php/dav/files/"+aliceUser+"/" {
		t.Errorf("calls = %+v", *calls)
	}
}

func excluding(t *testing.T, targets, excludes []string) (scope, error) {
	t.Helper()
	return parseScopeExcluding(targets, excludes)
}

func TestValidExclusionsAreParsedBelowTheirTargets(t *testing.T) {
	s, err := excluding(t, []string{"folder/Team", "notes/Work", "notes/Home"},
		[]string{"folder/Team/Private", "folder/Team/Archive/Old", "notes/Work/Secret", "notes/Home/A/B"})
	if err != nil {
		t.Fatalf("parseScopeExcluding = %v", err)
	}
	if len(s.excludes.folders) != 2 || len(s.excludes.notes) != 2 {
		t.Errorf("excludes = %+v", s.excludes)
	}
	if _, err := excluding(t, []string{"folder", "notes"}, []string{"folder/X", "notes/Y"}); err != nil {
		t.Errorf("below the whole root = %v", err)
	}
	// The legacy single target field is a folder target.
	r := resolvedConnection("x", "c", "u", "p", mainInstance, "Team")
	r.Exclude = []string{"folder/Team/Private"}
	if s, err := scopeOf(r); err != nil || !s.folderExcluded([]string{"Team", "Private", "x"}) {
		t.Errorf("legacy scopeOf = %+v, %v", s, err)
	}
	r.Exclude = []string{"folder/Other"}
	if _, err := scopeOf(r); err == nil {
		t.Error("legacy target accepted an exclusion outside it")
	}
}

func TestInvalidExclusionsFailClosedWithoutQuotingValues(t *testing.T) {
	many := make([]string, 0, 101)
	for i := 0; i <= 100; i++ {
		many = append(many, "folder/Team/"+strings.Repeat("x", i+1))
	}
	folder, notes := []string{"folder/Team", "notes/Work"}, []string{"folder", "notes"}
	tests := map[string]struct{ targets, excludes []string }{
		"kind alone folder":           {folder, []string{"folder"}},
		"kind alone notes":            {folder, []string{"notes"}},
		"kind alone calendar":         {[]string{"calendar"}, []string{"calendar"}},
		"account":                     {[]string{"folder", "account"}, []string{"account"}},
		"admin":                       {[]string{"admin"}, []string{"admin"}},
		"bare value":                  {folder, []string{"SECRET"}},
		"unknown kind":                {folder, []string{"mail/SECRET"}},
		"empty":                       {folder, []string{" "}},
		"empty path":                  {folder, []string{"folder/"}},
		"equal to folder target":      {folder, []string{"folder/Team"}},
		"above folder target":         {[]string{"folder/Team/Sub"}, []string{"folder/Team"}},
		"outside folder target":       {folder, []string{"folder/SECRET"}},
		"prefix only folder target":   {folder, []string{"folder/TeamSECRET"}},
		"no folder target":            {[]string{"notes"}, []string{"folder/SECRET"}},
		"absolute folder":             {folder, []string{"folder//SECRET"}},
		"traversal":                   {folder, []string{"folder/Team/../SECRET"}},
		"equal to category target":    {[]string{"notes/Work"}, []string{"notes/Work"}},
		"equal after normalisation":   {[]string{"notes/Work"}, []string{"notes/ .Work?"}},
		"outside category target":     {[]string{"notes/Work"}, []string{"notes/SECRET"}},
		"no notes target":             {folder, []string{"notes/SECRET"}},
		"normalises to the root":      {notes, []string{"notes/?"}},
		"duplicate":                   {folder, []string{"folder/Team/SECRET", "folder/Team/SECRET"}},
		"duplicate after fold":        {folder, []string{"folder/Team/SECRET", "folder/TEAM/secret"}},
		"duplicate after normal form": {folder, []string{"folder/Team/CaféSECRET", "folder/Team/CaféSECRET"}},
		"duplicate category":          {notes, []string{"notes/SECRET", "notes/s*ecret"}},
		"nested folder":               {folder, []string{"folder/Team/A", "folder/Team/A/SECRET"}},
		"nested folder reversed":      {folder, []string{"folder/Team/A/SECRET", "folder/Team/A"}},
		"nested category":             {notes, []string{"notes/A", "notes/A/SECRET"}},
		"too many":                    {[]string{"folder/Team"}, many},
		"calendar without target":     {folder, []string{"calendar/SECRET"}},
		"calendar below ID target":    {[]string{"calendar/x"}, []string{"calendar/SECRET"}},
		"addressbook below ID target": {[]string{"addressbook/x"}, []string{"addressbook/SECRET"}},
		"deck below ID target":        {[]string{"deck/1"}, []string{"deck/2"}},
		"calendar reserved":           {[]string{"calendar"}, []string{"calendar/inbox"}},
		"system addressbook":          {[]string{"addressbook"}, []string{"addressbook/" + systemAddressbook}},
		"deck not numeric":            {[]string{"deck"}, []string{"deck/SECRET"}},
		"talk below ID target":        {[]string{"talk/abcd1234"}, []string{"talk/SECRET"}},
		"talk without target":         {folder, []string{"talk/SECRET"}},
		"talk repeated by case":       {[]string{"talk"}, []string{"talk/SECRET", "talk/secret"}},
		"talk with slash":             {[]string{"talk"}, []string{"talk/SECRET/x"}},
		"account kind alone":          {[]string{"account"}, []string{"folder/"}},
		"account equal to target":     {[]string{"folder/Team", "account"}, []string{"folder/Team"}},
		"account notes equal target":  {[]string{"notes/Work", "account"}, []string{"notes/Work"}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := excluding(t, tc.targets, tc.excludes)
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "Team/") {
				t.Errorf("error quotes a value: %v", err)
			}
		})
	}
}

func TestIDExclusionsAreValidOnlyUnderTheWholeKind(t *testing.T) {
	for _, tc := range []struct{ target, exclude string }{
		{"calendar", "calendar/work"}, {"addressbook", "addressbook/book"}, {"deck", "deck/42"},
	} {
		if _, err := excluding(t, []string{tc.target}, []string{tc.exclude}); err != nil {
			t.Errorf("%s under %s = %v", tc.exclude, tc.target, err)
		}
		other := map[string]string{"calendar": "x1", "addressbook": "x1", "deck": "1"}[tc.target]
		if _, err := excluding(t, []string{tc.target + "/" + other}, []string{tc.exclude}); err != errExcludeInvalid {
			t.Errorf("%s under an ID target = %v", tc.exclude, err)
		}
		if _, err := excluding(t, []string{"folder"}, []string{tc.exclude}); err != errExcludeInvalid {
			t.Errorf("%s without a target = %v", tc.exclude, err)
		}
	}
	s, err := excluding(t, []string{"calendar", "addressbook", "deck"},
		[]string{"calendar/Work", "addressbook/Book", "deck/007"})
	if err != nil || len(s.excludes.calendars) != 1 || s.excludes.decks[0] != "7" {
		t.Errorf("scope = %+v, %v", s.excludes, err)
	}
}

func TestExclusionsOutOfReachAreInvalid(t *testing.T) {
	for _, tc := range []struct{ target, exclude string }{
		{"addressbook", "addressbook/" + systemAddressbook},
		{"addressbook", "addressbook/Z-Server-Generated--System"},
		{"calendar", "calendar/inbox"}, {"calendar", "calendar/OUTBOX"}, {"calendar", "calendar/Trashbin"},
		{"calendar", "calendar/work/x"}, {"deck", "deck/x"}, {"deck", "deck/-1"},
	} {
		_, err := excluding(t, []string{tc.target}, []string{tc.exclude})
		if err != errExcludeInvalid {
			t.Errorf("%s = %v, want invalid", tc.exclude, err)
		}
	}
	// A board ID repeats after its leading zeros are dropped.
	if _, err := excluding(t, []string{"deck"}, []string{"deck/7", "deck/07"}); err != errExcludeInvalid {
		t.Errorf("repeated board = %v", err)
	}
	if _, err := excluding(t, []string{"calendar"}, []string{"calendar/Work", "calendar/WORK"}); err != errExcludeInvalid {
		t.Errorf("repeated calendar = %v", err)
	}
}

func TestTalkExclusionsAreValidAndAccountTakesThem(t *testing.T) {
	for _, tc := range []struct{ targets, excludes []string }{
		{[]string{"talk"}, []string{"talk/token1"}},
		{[]string{"talk"}, []string{"talk/token1", "talk/Token2"}},
		{[]string{"folder/Team", "talk"}, []string{"folder/Team/A"}},
		{[]string{"folder/Team", "talk"}, []string{"folder/Team/A", "talk/token1"}},
		{[]string{"notes", "talk"}, []string{"notes/A"}},
		{[]string{"calendar", "talk"}, []string{"calendar/A"}},
	} {
		if _, err := excluding(t, tc.targets, tc.excludes); err != nil {
			t.Errorf("%v with %v = %v", tc.targets, tc.excludes, err)
		}
	}
	for _, tc := range []struct{ targets, excludes []string }{
		{[]string{"folder", "account"}, []string{"folder/A"}},
		{[]string{"calendar", "account"}, []string{"calendar/A"}},
		{[]string{"talk", "account"}, []string{"talk/token1"}},
	} {
		if _, err := excluding(t, tc.targets, tc.excludes); err != nil {
			t.Errorf("%v beside account = %v", tc.targets, err)
		}
	}
}

func TestAccountTakesEveryExclusionKindWithoutATargetOfThatKind(t *testing.T) {
	s, err := excluding(t, []string{"account"}, []string{"folder/A/B", "notes/Work", "calendar/Work", "addressbook/Book",
		"deck/07", "talk/Token1"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.account || s.hasFolder || s.notes.bound() || s.calendars.bound() || s.addressbooks.bound() || s.talks.bound() || s.decks.bound() {
		t.Errorf("exclusions changed what the targets bind: %+v", s)
	}
	if !s.crossNeeded() {
		t.Error("folder and notes exclusions reach each other through the account tools")
	}
	if !s.feedActive() || !s.talkHides("TOKEN1") || !s.folderExcluded([]string{"a", "B", "x"}) || !s.categoryExcluded("work/sub") {
		t.Errorf("exclusions do not take effect: %+v", s.excludes)
	}
	// With a target of the kind, the exclusion still lies strictly below it.
	for _, tc := range []struct {
		targets, excludes []string
		valid             bool
	}{
		{[]string{"account", "folder/Team"}, []string{"folder/Team/A"}, true},
		{[]string{"account", "folder/Team"}, []string{"folder/Team"}, false},
		{[]string{"account", "folder/Team"}, []string{"folder/Other"}, false},
		{[]string{"account", "notes/Work"}, []string{"notes/Work"}, false},
		{[]string{"account", "notes/Work"}, []string{"notes/Work/A"}, true},
		{[]string{"account", "calendar/x"}, []string{"calendar/y"}, false},
		{[]string{"account", "deck/1"}, []string{"deck/2"}, false},
		{[]string{"account", "talk/abcd1234"}, []string{"talk/other"}, false},
		{[]string{"account"}, []string{"folder"}, false},
		{[]string{"account"}, []string{"account"}, false},
		{[]string{"account"}, []string{"calendar/inbox"}, false},
		{[]string{"account"}, []string{"addressbook/" + systemAddressbook}, false},
		{[]string{"account"}, []string{"folder/A", "folder/A/B"}, false},
		{[]string{"account"}, []string{"deck/7", "deck/07"}, false},
		{[]string{"account"}, []string{"deck/x"}, false},
	} {
		_, err := excluding(t, tc.targets, tc.excludes)
		if (err == nil) != tc.valid || err != nil && (err != errExcludeInvalid || strings.Contains(err.Error(), "Team")) {
			t.Errorf("%v with %v = %v, want valid %t", tc.targets, tc.excludes, err, tc.valid)
		}
	}
	many := make([]string, 0, 101)
	for i := 0; i <= 100; i++ {
		many = append(many, "folder/"+strings.Repeat("x", i+1))
	}
	if _, err := excluding(t, []string{"account"}, many); err != errExcludeTooMany {
		t.Errorf("too many = %v", err)
	}
	// A connection without the account target keeps the rule of a target of the kind.
	if _, err := excluding(t, []string{"folder"}, []string{"calendar/A"}); err != errExcludeInvalid {
		t.Errorf("without account = %v", err)
	}
}

func TestExclusionsNeverChangeTheTargetsOrReach(t *testing.T) {
	with, err := excluding(t, []string{"folder/Team", "calendar"}, []string{"folder/Team/A"})
	if err != nil {
		t.Fatal(err)
	}
	without, _ := parseScope([]string{"folder/Team", "calendar"})
	with.excludes = exclusions{}
	if !reflect.DeepEqual(with, without) {
		t.Errorf("excludes changed the targets: %+v vs %+v", with, without)
	}
}

func TestExclusionComparisonForm(t *testing.T) {
	s, err := excluding(t, []string{"folder/Team"}, []string{"folder/Team/Café/Reports"})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{
		"Team/Café/Reports":              true,
		"Team/Café/Reports":             true, // NFD spelling
		"Team/CAFÉ/reports/deeper/file": true,
		"Team/Café/Reports2":             false,
		"Team/Café":                      false,
		"Team":                           false,
	} {
		if got := s.folderExcluded(strings.Split(path, "/")); got != want {
			t.Errorf("folderExcluded(%q) = %v, want %v", path, got, want)
		}
	}
	for path, want := range map[string]bool{
		"Team": true, "team/CAFÉ": true, "Team/Café/Reports": false, "Team/Café/Reports/x": false, "Other": false,
	} {
		if got := s.folderHoldsExcluded(strings.Split(path, "/")); got != want {
			t.Errorf("folderHoldsExcluded(%q) = %v, want %v", path, got, want)
		}
	}
	// Folding is full Unicode folding.
	f, err := excluding(t, []string{"folder"}, []string{"folder/Straße"})
	if err != nil || !f.folderExcluded([]string{"STRASSE", "x"}) {
		t.Errorf("full folding: %v", err)
	}
}

func TestSegmentwiseComparisonHasNoPrefixMismatch(t *testing.T) {
	s, err := excluding(t, []string{"folder", "notes"}, []string{"folder/a/b", "notes/a/b"})
	if err != nil {
		t.Fatal(err)
	}
	if s.folderExcluded([]string{"a", "bc"}) || s.folderHoldsExcluded([]string{"a", "bc"}) ||
		s.categoryExcluded("a/bc") || s.categoryHoldsExcluded("a/bc") {
		t.Error("a/bc matched a/b")
	}
	if !s.folderExcluded([]string{"a", "b", "c"}) || !s.folderHoldsExcluded([]string{"a"}) ||
		!s.categoryExcluded("a/b/c") || !s.categoryHoldsExcluded("a") {
		t.Error("segment match missed")
	}
	if s.folderHoldsExcluded([]string{"a", "b"}) || s.categoryHoldsExcluded("a/b") {
		t.Error("an exclusion is not its own ancestor")
	}
	if !s.folderHoldsExcluded(nil) || !s.categoryHoldsExcluded("") {
		t.Error("the root holds an exclusion")
	}
}

func TestNotesCategoryNormalisation(t *testing.T) {
	for in, want := range map[string]string{
		"Work/Plans":       "work/plans",
		" .Work ?/*Plans|": "work/plans",
		"A//B":             "a/b",
		"a:b/\"c\"<d>":     "ab/cd",
		"..hidden/\t x ":   "hidden/x",
		"?/*":              "",
		"Café/ORDERS":     "café/orders",
		"trailing.dot./x":  "trailing.dot./x",
	} {
		if got := strings.Join(normalizeCategory(in), "/"); got != want {
			t.Errorf("normalizeCategory(%q) = %q, want %q", in, got, want)
		}
	}
	s, err := excluding(t, []string{"notes/Work"}, []string{"notes/Work/Se:cret|"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.categoryExcluded("Work/ .Secret/inner") || !s.categoryExcluded("WORK/SECRET") || s.categoryExcluded("Work/Secrets") {
		t.Error("category match after normalisation is wrong")
	}
}

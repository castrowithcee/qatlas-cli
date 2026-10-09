package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
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
	class, err := TestConnection(context.Background(), listed("calendar", "account"), resolver(red), red)
	if err != nil || class != provider.ClassOK {
		t.Fatalf("TestConnection = %q, %v", class, err)
	}
	if len(*calls) != 1 || (*calls)[0].method != "PROPFIND" || (*calls)[0].depth != depthSelf ||
		(*calls)[0].url.Path != "/remote.php/dav/files/"+aliceUser+"/" {
		t.Errorf("calls = %+v", *calls)
	}
}

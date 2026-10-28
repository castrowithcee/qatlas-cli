package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// excluderMetadata requires a target, takes several and declares exclusions; soloMetadata takes one target
// and declares exclusions. bookMetadata declares none.
var excluderMetadata = config.ProviderMetadata{
	ID: "excluder", Name: "Excluder", DefaultEffects: []config.Effect{config.EffectRead},
	SupportedEffects: []config.Effect{config.EffectRead},
	SecretRoles:      []config.SecretRole{{Name: "token", Description: "the token"}},
	Target: config.TargetMetadata{Label: "repository", Required: true, Multiple: true, Wildcard: "*",
		Kinds: []config.TargetKind{{Name: "repo", Forms: []string{"repos/OWNER/REPO", "repos/OWNER/*"}, Excludable: true}}},
	Tools: []config.ToolMetadata{{ID: "ex.read", Title: "Read", Effect: config.EffectRead}},
}

var soloMetadata = config.ProviderMetadata{
	ID: "solo", Name: "Solo", DefaultEffects: []config.Effect{config.EffectRead},
	SupportedEffects: []config.Effect{config.EffectRead},
	SecretRoles:      []config.SecretRole{{Name: "token", Description: "the token"}},
	Target: config.TargetMetadata{Label: "folder", Wildcard: "*",
		Kinds: []config.TargetKind{{Name: "folder", Forms: []string{"f/NAME"}, Excludable: true}}},
	Tools: []config.ToolMetadata{{ID: "solo.read", Title: "Read", Effect: config.EffectRead}},
}

func excludeForm(provider, tool string, extra url.Values) url.Values {
	form := url.Values{
		"provider": {provider}, "service": {newServiceChoice}, "svcname": {provider + "-cloud"},
		"svcbaseurl": {"https://x.example.test"},
		"credential": {newCredentialChoice}, "credname": {provider + "-cred"}, "credstorage": {"env"},
		"envname_token": {"X_TOKEN"}, "connname": {provider + "-conn"}, "permmode": {"default"},
		"access": {"tools"}, "tool": {tool},
	}
	for k, v := range extra {
		form[k] = v
	}
	return form
}

func TestGuidedConnectionSplitsTargetsAndExclusionsWithLabels(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	form := excludeForm("excluder", "ex.read", url.Values{"targets": {"*"}, "exclude": {"repos/o/a, repos/o/b"},
		"label_*": {"Everything"}, "label_repos/o/a": {"Secret"}})
	review := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil)
	body := review.Body.String()
	if !strings.Contains(body, "repos/o/a (Secret), repos/o/b") || !strings.Contains(body, `name="exclude" value="repos/o/a, repos/o/b"`) {
		t.Fatalf("review lacks the exclusions: %s", body)
	}
	rec := saveLabelForm(t, s, cookie, csrf, form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	conn := cfg.Connections["excluder-conn"]
	if !reflect.DeepEqual(conn.TargetValues(), []string{"*"}) || !reflect.DeepEqual(conn.Exclude, []string{"repos/o/a", "repos/o/b"}) {
		t.Fatalf("targets = %v, exclude = %v", conn.TargetValues(), conn.Exclude)
	}
	if want := map[string]string{"*": "Everything", "repos/o/a": "Secret"}; !reflect.DeepEqual(conn.TargetLabels, want) {
		t.Fatalf("labels = %v, want %v", conn.TargetLabels, want)
	}
	result := s.request(t, http.MethodGet, "/connections/excluder-conn", s.addr, cookie, nil).Body.String()
	if !strings.Contains(result, "Exclusions") || !strings.Contains(result, "repos/o/a (Secret), repos/o/b") {
		t.Fatalf("result page lacks the exclusions: %s", result)
	}
}

func TestGuidedConnectionRowSwitchMovesEntries(t *testing.T) {
	s, _, _, _ := newConnectionTestServer(t, nil)
	cookie, _ := coupleAndApprove(t, s, nil)
	form := excludeForm("excluder", "ex.read", url.Values{"targets": {"*, repos/o/a"}, "exclude": {"repos/o/b"},
		"switch_repos/o/a": {"1"}, "switch_repos/o/b": {"1"}})
	review := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil).Body.String()
	if !strings.Contains(review, `name="targets" value="*, repos/o/b"`) || !strings.Contains(review, `name="exclude" value="repos/o/a"`) {
		t.Fatalf("switch not applied on the review page: %s", review)
	}

	build := s.request(t, http.MethodGet, "/connections/new?"+excludeForm("excluder", "ex.read",
		url.Values{"access": {"tools"}, "targets": {"*, repos/o/a"}, "exclude": {"repos/o/b"}}).Encode(), s.addr, cookie, nil).Body.String()
	for _, want := range []string{`name="exclude"`, `name="switch_repos/o/a"`, `name="switch_repos/o/b"`} {
		if !strings.Contains(build, want) {
			t.Fatalf("build page lacks %s: %s", want, build)
		}
	}
	// The wildcard row is never excludable.
	if strings.Contains(build, `name="switch_*"`) {
		t.Fatalf("wildcard row offers a switch: %s", build)
	}
}

func TestGuidedConnectionWithoutDeclaredExclusionsHasNoSwitch(t *testing.T) {
	s, _, _, _ := newConnectionTestServer(t, nil)
	cookie, _ := coupleAndApprove(t, s, nil)
	build := s.request(t, http.MethodGet, "/connections/new?provider=book&access=effects&targets=a,b", s.addr, cookie, nil).Body.String()
	if strings.Contains(build, `name="exclude"`) || strings.Contains(build, "switch_") || strings.Contains(build, `data-excludes="true"`) {
		t.Fatalf("a provider without exclusions shows exclusion controls: %s", build)
	}
	// An exclusion sent anyway is refused by the core.
	form := labelForm(url.Values{"targets": {"a"}, "exclude": {"b"}})
	rec := s.request(t, http.MethodGet, "/connections/new/review?"+form.Encode(), s.addr, cookie, nil)
	if !strings.Contains(rec.Body.String(), "does not support exclusions") {
		t.Fatalf("exclusion of a provider without them not refused: %s", rec.Body.String())
	}
}

func TestGuidedConnectionSingleTargetKeepsOneTargetAndManyExclusions(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	form := excludeForm("solo", "solo.read", url.Values{"targets": {"*"}, "exclude": {"f/a, f/b"}})
	if rec := saveLabelForm(t, s, cookie, csrf, form); rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	cfg, _ := store.Load()
	conn := cfg.Connections["solo-conn"]
	if !reflect.DeepEqual(conn.TargetValues(), []string{"*"}) || len(conn.Exclude) != 2 {
		t.Fatalf("targets = %v, exclude = %v", conn.TargetValues(), conn.Exclude)
	}
	two := excludeForm("solo", "solo.read", url.Values{"targets": {"f/a, f/b"}})
	rec := s.request(t, http.MethodGet, "/connections/new/review?"+two.Encode(), s.addr, cookie, nil)
	if strings.Contains(rec.Body.String(), "Create connection") {
		t.Fatalf("two targets accepted for a one-target provider: %s", rec.Body.String())
	}
}

func TestGuidedConnectionOfferedToolsAreTheSameWithExclusions(t *testing.T) {
	s, store, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	for _, c := range []struct {
		name  string
		extra url.Values
	}{
		{"plain", url.Values{"targets": {"repos/o/*"}}},
		{"with", url.Values{"targets": {"repos/o/*"}, "exclude": {"repos/o/x"}, "connname": {"with"}, "credname": {"c2"}, "svcname": {"s2"}}},
	} {
		form := excludeForm("excluder", "ex.read", c.extra)
		if c.name == "plain" {
			form.Set("connname", "plain")
		}
		if rec := saveLabelForm(t, s, cookie, csrf, form); rec.Code != http.StatusSeeOther {
			t.Fatalf("%s: status = %d, body: %s", c.name, rec.Code, rec.Body.String())
		}
	}
	cfg, _ := store.Load()
	if !reflect.DeepEqual(cfg.Connections["plain"].Tools, cfg.Connections["with"].Tools) || len(cfg.Connections["with"].Tools) != 1 {
		t.Fatalf("tools differ: %v vs %v", cfg.Connections["plain"].Tools, cfg.Connections["with"].Tools)
	}
}

func TestTargetListMarksExcludableEntries(t *testing.T) {
	s, _, _, _ := newConnectionTestServer(t, nil)
	cookie, csrf := coupleAndApprove(t, s, nil)
	entries := []config.TargetEntry{{Target: "repos/o/a", Name: "A", Kind: "repo"}}
	src := bookTargetSource(entries, nil)
	src.Browses = func(id string) bool { return id == "excluder" }
	s.svc = s.svc.WithTargetSource(src)
	form := excludeForm("excluder", "ex.read", url.Values{"kind": {"repo"}})
	rec := s.postForm(t, targetListPath, s.addr, cookie, "http://"+s.addr, csrf, form)
	var got targetListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v, body %s", err, rec.Body.String())
	}
	if len(got.Entries) != 1 || !got.Entries[0].Excludable {
		t.Fatalf("entries = %+v, want an excludable entry", got.Entries)
	}
	build := s.request(t, http.MethodGet, "/connections/new?provider=excluder&access=tools", s.addr, cookie, nil).Body.String()
	if !strings.Contains(build, `data-excludes="true"`) {
		t.Fatalf("dialog lacks the exclusion mark: %s", build)
	}
	// A provider that declares no exclusions marks nothing.
	s.svc = s.svc.WithTargetSource(bookTargetSource([]config.TargetEntry{{Target: "shelf/a", Name: "A", Kind: "shelf"}}, nil))
	rec = s.postForm(t, targetListPath, s.addr, cookie, "http://"+s.addr, csrf, draftForm())
	got = targetListResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Entries) != 1 || got.Entries[0].Excludable {
		t.Fatalf("book entries = %+v (%v), want none excludable", got.Entries, err)
	}
}

package todoist

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// browseFake serves a small account: Work holds Plans, which holds Q4; Home is top level. The list route
// answers two projects per page, so a full read takes several pages.
type browseFake struct {
	mu    sync.Mutex
	paths []string
	auths []string
	query []url.Values
}

func (f *browseFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.auths = append(f.auths, r.Header.Get("Authorization"))
	f.query = append(f.query, r.URL.Query())
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	rows := func(all []string) {
		page := 0
		if c := r.URL.Query().Get("cursor"); c != "" {
			fmt.Sscanf(c, "c%d", &page)
		}
		const size = 2
		end := min((page+1)*size, len(all))
		next := "null"
		if end < len(all) {
			next = fmt.Sprintf(`"c%d"`, page+1)
		}
		fmt.Fprintf(w, `{"results":[%s],"next_cursor":%s}`, strings.Join(all[min(page*size, end):end], ","), next)
	}
	project := func(id, name, parent string) string {
		p := "null"
		if parent != "" {
			p = `"` + parent + `"`
		}
		return fmt.Sprintf(`{"id":%q,"name":%q,"parent_id":%s}`, id, name, p)
	}
	all := []string{project("w1", "Work", ""), project("h1", "Home\nstead", ""), project("p1", "Plans", "w1"),
		`{"id":"d1","name":"Gone","parent_id":null,"is_deleted":true}`, project("q1", "Q4", "p1"),
		`{"id":"bad id","name":"Bad","parent_id":null}`}
	switch {
	case r.URL.Path == "/api/v1/projects":
		rows(all)
	case r.URL.Path == "/api/v1/projects/search":
		if r.URL.Query().Get("query") == "Q" {
			rows([]string{project("q1", "Q4", "p1"), project("w1", "Work", "")})
		} else {
			rows(nil)
		}
	case r.URL.Path == "/api/v1/projects/w1":
		fmt.Fprint(w, project("w1", "Work", ""))
	case r.URL.Path == "/api/v1/sections":
		rows([]string{`{"id":"s1","project_id":"w1","name":"Todo","section_order":1}`,
			`{"id":"s2","project_id":"other","name":"Foreign","section_order":2}`,
			`{"id":"s3","project_id":"w1","name":"Done","section_order":3,"is_archived":true}`,
			`{"id":"s4","project_id":"w1","name":"Doing","section_order":4}`})
	case r.URL.Path == "/api/v1/tasks/t1":
		fmt.Fprint(w, `{"id":"t1","project_id":"p1","section_id":"s9","content":"x"}`)
	case r.URL.Path == "/api/v1/tasks/t2":
		fmt.Fprint(w, `{"id":"t2","project_id":"p1","section_id":null,"content":"x"}`)
	default:
		http.NotFound(w, r)
	}
}

func browseResolved(targets ...string) *config.Resolved {
	return &config.Resolved{Name: "td", Provider: Provider, BaseURL: apiRoot, Targets: targets, Credential: "td-reader",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleToken: tokenEnv}}}
}

func browseCall(t *testing.T, f *browseFake, req config.TargetListRequest, targets ...string) (config.TargetPage, error) {
	t.Helper()
	serve(t, f)
	red := &redact.Redactor{}
	return browseList(context.Background(), browseResolved(targets...), resolver(red, nil), red, req)
}

func targetsOf(page config.TargetPage) []string {
	var out []string
	for _, e := range page.Entries {
		out = append(out, e.Target+"|"+e.Name+"|"+e.Path)
	}
	return out
}

func TestBrowseProjectTreeAcrossPages(t *testing.T) {
	f := &browseFake{}
	serve(t, f)
	red := &redact.Redactor{}
	list := func(req config.TargetListRequest) config.TargetPage {
		t.Helper()
		page, err := browseList(context.Background(), browseResolved("zzOutside"), resolver(red, nil), red, req)
		if err != nil {
			t.Fatalf("%+v: %v", req, err)
		}
		return page
	}
	// Top level over three pages of two; a connection bound elsewhere does not narrow the offer.
	req := config.TargetListRequest{Kind: "project"}
	var got []string
	pages := 0
	for {
		page := list(req)
		pages++
		got = append(got, targetsOf(page)...)
		if page.ServerSearch {
			t.Error("a plain list reports a server search")
		}
		if page.Next == "" {
			break
		}
		req.Cursor = page.Next
	}
	if pages != 3 || !slices.Equal(got, []string{"project/w1|Work|", "project/h1|Home stead|"}) {
		t.Fatalf("pages = %d, entries = %v", pages, got)
	}
	for _, a := range f.auths {
		if a != "Bearer "+tokenValue {
			t.Errorf("auth = %q", a)
		}
	}
	// Children: the path shows the chain.
	page := list(config.TargetListRequest{Kind: "project", Parent: "project/p1"})
	if !slices.Equal(targetsOf(page), nil) {
		t.Fatalf("first child page = %v", targetsOf(page))
	}
	req = config.TargetListRequest{Kind: "project", Parent: "p1"}
	got = nil
	for {
		page := list(req)
		got = append(got, targetsOf(page)...)
		if page.Next == "" {
			break
		}
		req.Cursor = page.Next
	}
	if !slices.Equal(got, []string{"project/q1|Q4|Work / Plans"}) {
		t.Fatalf("children = %v", got)
	}
	page = list(config.TargetListRequest{Kind: "project", Parent: "project/w1"})
	if page.Next == "" || len(page.Entries) != 0 {
		t.Fatalf("first page below w1 = %+v, want an empty page with a next page", page)
	}
	page = list(config.TargetListRequest{Kind: "project", Parent: "project/w1", Cursor: page.Next})
	if !slices.Equal(targetsOf(page), []string{"project/p1|Plans|Work"}) {
		t.Fatalf("children of w1 = %v", targetsOf(page))
	}
}

func TestBrowseProjectSearchRunsAtTheServer(t *testing.T) {
	f := &browseFake{}
	page, err := browseCall(t, f, config.TargetListRequest{Kind: "project", Query: "Q"})
	if err != nil || !page.ServerSearch || page.Next != "" ||
		!slices.Equal(targetsOf(page), []string{"project/q1|Q4|Work / Plans", "project/w1|Work|"}) {
		t.Fatalf("search = %+v, %v", page, err)
	}
	if f.paths[0] != "/api/v1/projects/search" || f.query[0].Get("query") != "Q" || f.query[0].Get("limit") != "50" {
		t.Errorf("first request = %s %v", f.paths[0], f.query[0])
	}
}

func TestBrowseSectionsOfAProject(t *testing.T) {
	f := &browseFake{}
	serve(t, f)
	red := &redact.Redactor{}
	req := config.TargetListRequest{Kind: "section", Parent: "project/w1"}
	var got []string
	for {
		page, err := browseList(context.Background(), browseResolved("otherProject"), resolver(red, nil), red, req)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, targetsOf(page)...)
		if page.Next == "" {
			break
		}
		req.Cursor = page.Next
	}
	if !slices.Equal(got, []string{"section/s1|Todo|Work", "section/s4|Doing|Work"}) {
		t.Fatalf("sections = %v", got)
	}
	for i, p := range f.paths {
		if p == "/api/v1/sections" && f.query[i].Get("project_id") != "w1" {
			t.Errorf("project_id = %q", f.query[i].Get("project_id"))
		}
	}
}

func TestBrowseRefusesWrongRequests(t *testing.T) {
	f := &browseFake{}
	for name, req := range map[string]config.TargetListRequest{
		"section without parent": {Kind: "section"},
		"section parent":         {Kind: "section", Parent: "section/s1"},
		"wildcard parent":        {Kind: "project", Parent: "*"},
		"unknown kind":           {Kind: "label"},
		"foreign cursor":         {Kind: "project", Cursor: "AAAAAAAAAAAAAAAAAAAA"},
	} {
		if _, err := browseCall(t, f, req); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want invalid request", name, err)
		}
	}
	if len(f.paths) != 0 {
		t.Errorf("requests = %v, want none", f.paths)
	}
}

func TestBrowseResolveURL(t *testing.T) {
	for name, test := range map[string]struct {
		url  string
		want []string
		read string
	}{
		"project":            {"https://app.todoist.com/app/project/work-plans-w1", []string{"project/w1"}, ""},
		"project id only":    {"https://app.todoist.com/app/project/w1", []string{"project/w1"}, ""},
		"project trailing /": {"https://app.todoist.com/app/project/work-w1/?x=1#y", []string{"project/w1"}, ""},
		"upper host":         {"https://APP.todoist.com/app/project/work-w1", []string{"project/w1"}, ""},
		"task with section":  {"https://app.todoist.com/app/task/buy-the-milk-t1", []string{"project/p1", "section/s9"}, "/api/v1/tasks/t1"},
		"task no section":    {"https://app.todoist.com/app/task/t2", []string{"project/p1"}, "/api/v1/tasks/t2"},
		"suffix host":        {"https://app.todoist.com.evil.example/app/project/work-w1", nil, ""},
		"prefix host":        {"https://evilapp.todoist.com/app/project/work-w1", nil, ""},
		"other host":         {"https://todoist.com/app/project/work-w1", nil, ""},
		"port":               {"https://app.todoist.com:8443/app/project/work-w1", nil, ""},
		"credentials": {(&url.URL{Scheme: "https", User: url.User("app.todoist.com"), Host: "evil.example",
			Path: "/app/project/work-w1"}).String(), nil, ""},
		"http":             {"http://app.todoist.com/app/project/work-w1", nil, ""},
		"api host":         {"https://api.todoist.com/api/v1/projects/w1", nil, ""},
		"label page":       {"https://app.todoist.com/app/label/work-w1", nil, ""},
		"today":            {"https://app.todoist.com/app/today", nil, ""},
		"project sub page": {"https://app.todoist.com/app/project/work-w1/extra", nil, ""},
		"no app":           {"https://app.todoist.com/project/work-w1", nil, ""},
		"bad id":           {"https://app.todoist.com/app/project/work-w%201", nil, ""},
		"empty id":         {"https://app.todoist.com/app/project/work-", nil, ""},
		"escaped slash":    {"https://app.todoist.com/app/project/work%2Fw1", nil, ""},
		"not a url":        {"://", nil, ""},
	} {
		f := &browseFake{}
		serve(t, f)
		red := &redact.Redactor{}
		entries, err := browseResolveURL(context.Background(), browseResolved(), resolver(red, nil), red, apiRoot, test.url)
		var got []string
		for _, e := range entries {
			got = append(got, e.Target)
		}
		if err != nil || !slices.Equal(got, test.want) {
			t.Errorf("%s: entries = %v, %v, want %v", name, got, err, test.want)
		}
		wantPaths := []string(nil)
		if test.read != "" {
			wantPaths = []string{test.read}
		}
		if !slices.Equal(f.paths, wantPaths) {
			t.Errorf("%s: requests = %v, want %v", name, f.paths, wantPaths)
		}
	}
}

func TestBrowseRegistered(t *testing.T) {
	if !registry(t).HasTargetBrowser(Provider) {
		t.Fatal("no target browser")
	}
}

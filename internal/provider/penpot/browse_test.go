package penpot

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func browseResolved() *config.Resolved {
	return &config.Resolved{Name: "x", Provider: Provider, BaseURL: baseURL, Credential: "token",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAccessToken: tokenEnv}}}
}

func browseHandler(c call) (*http.Response, error) {
	switch c.command() {
	case cmdTeams:
		return jsonResponse(200, `[{"id":"`+teamA+`","name":"Agency\u0007 `+strings.Repeat("x", 300)+`"},{"id":"`+teamB+`","name":"Print"},{"id":"bad","name":"Skip"}]`), nil
	case cmdProjects:
		return projectsHandler(c)
	case cmdProjectFile:
		return filesHandler(0)(c)
	}
	return jsonResponse(404, bodyCanary), nil
}

func browse(t *testing.T, calls *[]call, kind, parent string) (config.TargetPage, error) {
	t.Helper()
	serve(t, calls, browseHandler)
	red := &redact.Redactor{}
	return browseList(context.Background(), browseResolved(), resolver(red, nil), red,
		config.TargetListRequest{Kind: kind, Parent: parent})
}

func targetsOf(page config.TargetPage) []string {
	var out []string
	for _, e := range page.Entries {
		out = append(out, e.Target)
	}
	return out
}

func TestBrowseLevelsListWithoutTargetFilter(t *testing.T) {
	// The connection has no targets yet: the lists read beyond them.
	var calls []call
	page, err := browse(t, &calls, "team", "")
	if err != nil || !slices.Equal(targetsOf(page), []string{"team/" + teamA, "team/" + teamB}) || page.Next != "" {
		t.Fatalf("teams = %+v, %v", page, err)
	}
	if name := page.Entries[0].Name; strings.ContainsRune(name, 7) || len(name) > maxBrowseText || !page.Entries[0].HasChildren {
		t.Fatalf("name = %q", name)
	}
	calls = nil
	page, err = browse(t, &calls, "project", "team/"+teamA)
	if err != nil || !slices.Equal(targetsOf(page), []string{"project/" + projectA1, "project/" + projectA2}) ||
		!page.Entries[0].HasChildren || commands(calls)[0] != cmdProjects || calls[0].body["team-id"] != teamA {
		t.Fatalf("projects = %+v, %v, %v", page, err, commands(calls))
	}
	calls = nil
	page, err = browse(t, &calls, "file", "project/"+projectA1)
	if err != nil || !slices.Equal(targetsOf(page), []string{"file/" + fileA1}) || page.Entries[0].Kind != "file" ||
		page.Entries[0].HasChildren || calls[0].body["project-id"] != projectA1 {
		t.Fatalf("files = %+v, %v", page, err)
	}
}

func TestBrowseRefusesBadKindAndParent(t *testing.T) {
	for _, test := range []struct{ kind, parent string }{
		{"project", ""}, {"project", "project/" + projectA1}, {"file", "team/" + teamA}, {"file", "project/x"}, {"drive", ""},
	} {
		var calls []call
		_, err := browse(t, &calls, test.kind, test.parent)
		if !isInvalidRequest(err) || len(calls) != 0 {
			t.Errorf("%+v: err = %v, calls = %d", test, err, len(calls))
		}
	}
}

func TestBrowseResolveURL(t *testing.T) {
	other := "00000000-0000-0000-0000-0000000000aa"
	for name, test := range map[string]struct {
		origin, url string
		want        []string
	}{
		"dashboard project": {baseURL, baseURL + "/#/dashboard/files?team-id=" + teamA + "&project-id=" + projectA1,
			[]string{"team/" + teamA, "project/" + projectA1}},
		"dashboard team": {baseURL, baseURL + "/#/dashboard/recent?team-id=" + teamA, []string{"team/" + teamA}},
		"legacy dashboard": {baseURL, baseURL + "/#/dashboard/team/" + teamA + "/projects/" + projectA1,
			[]string{"team/" + teamA, "project/" + projectA1}},
		"workspace": {baseURL, baseURL + "/#/workspace?team-id=" + teamA + "&file-id=" + fileA1 + "&page-id=" + pageID,
			[]string{"team/" + teamA, "file/" + fileA1}},
		"workspace without team": {baseURL, baseURL + "/#/workspace?file-id=" + fileA1, []string{"file/" + fileA1}},
		"upper case ids":         {baseURL, baseURL + "/#/workspace?file-id=" + strings.ToUpper(fileA1), []string{"file/" + fileA1}},
		"cloud default":          {"", "https://design.penpot.app/#/dashboard/files?team-id=" + teamA, []string{"team/" + teamA}},
		"self-hosted path":       {baseURL + "/penpot", baseURL + "/penpot/#/workspace?file-id=" + fileA1, []string{"file/" + fileA1}},
		"foreign origin":         {baseURL, "https://other.example.test/#/workspace?file-id=" + fileA1, nil},
		"cloud url, own origin":  {baseURL, "https://design.penpot.app/#/workspace?file-id=" + fileA1, nil},
		"http":                   {baseURL, "http://" + apiHost + "/#/workspace?file-id=" + fileA1, nil},
		"missing ids":            {baseURL, baseURL + "/#/workspace", nil},
		"bad ids":                {baseURL, baseURL + "/#/workspace?team-id=x&file-id=y", nil},
		"no fragment":            {baseURL, baseURL + "/?file-id=" + fileA1, nil},
		"other route":            {baseURL, baseURL + "/#/view?file-id=" + fileA1 + "&team-id=" + other, nil},
		"project without team":   {baseURL, baseURL + "/#/dashboard/files?project-id=" + projectA1, []string{"project/" + projectA1}},
	} {
		var calls []call
		serve(t, &calls, browseHandler)
		red := &redact.Redactor{}
		entries, err := browseResolveURL(context.Background(), browseResolved(), resolver(red, nil), red, test.origin, test.url)
		var got []string
		for _, e := range entries {
			got = append(got, e.Target)
		}
		if err != nil || !slices.Equal(got, test.want) || len(calls) != 0 {
			t.Errorf("%s: got %v, %v, calls %d, want %v", name, got, err, len(calls), test.want)
		}
	}
}

func TestReaches(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	fileOnly := []string{"team/" + teamA, "file/" + fileA1}
	for _, tool := range metadata.Tools {
		for name, targets := range map[string][]string{"team": {"team/" + teamA}, "project": {"team/" + teamA, "project/" + projectA1},
			"mixed": {"team/" + teamA, "project/" + projectA1, "file/" + fileA1}} {
			if reached, hint := metadata.Target.Reach(tool.ID, targets); !reached || hint != "" {
				t.Errorf("%s %s: reached = %t, %q, want reached", name, tool.ID, reached, hint)
			}
		}
		reached, hint := metadata.Target.Reach(tool.ID, fileOnly)
		want := map[string]string{
			filesList.ID: "project/PROJECT_ID", projectsList.ID: "project/PROJECT_ID", projectsRename.ID: "project/PROJECT_ID",
			projectsDelete.ID: "project/PROJECT_ID", filesCreate.ID: "project/PROJECT_ID", filesImport.ID: "project/PROJECT_ID",
			filesMove.ID: "project/PROJECT_ID", projectsCreate.ID: "team/TEAM_ID", membersList.ID: "team/TEAM_ID",
			membersSetRole.ID: "team/TEAM_ID", membersRemove.ID: "team/TEAM_ID", webhooksList.ID: "team/TEAM_ID",
			webhooksUpdate.ID: "team/TEAM_ID", webhooksDelete.ID: "team/TEAM_ID", teamsCreate.ID: "team/TEAM_ID",
			teamsDelete.ID: "team/TEAM_ID"}[tool.ID]
		if want == "" && (!reached || hint != "") || want != "" && (reached || hint != "needs "+want) {
			t.Errorf("file %s: reached = %t, %q, want need %q", tool.ID, reached, hint, want)
		}
	}
}

package seatableaccount

import (
	"context"
	"net/http"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

func browseResolved() *config.Resolved {
	return &config.Resolved{Name: "reader", Provider: Provider, BaseURL: baseURL, Service: "account", Credential: "token",
		Secrets: config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{roleAccountToken: tokenEnv}}}
}

func TestListTargetsListsBasesWithoutTargetFilter(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"workspace_list":[{"id":42,"name":"w","type":"personal","table_list":[
{"workspace_id":42,"name":"My Base"},{"workspace_id":42,"name":"a\u0000b"},{"workspace_id":42,"name":"x/y"}]},
{"id":7,"name":"g","type":"group","table_list":[{"workspace_id":7,"name":"Other"}]}]}`), nil
	})
	red := &redact.Redactor{}
	page, err := listTargets(context.Background(), browseResolved(), resolver(red, nil), red, config.TargetListRequest{Kind: "base"})
	if err != nil || len(page.Entries) != 2 || page.Entries[0].Target != "42/My Base" || page.Entries[1].Target != "7/Other" {
		t.Fatalf("page = %+v, %v", page, err)
	}
	if len(calls) != 1 || calls[0].path != "/api/v2.1/workspaces/" || calls[0].auth != "Bearer "+tokenValue {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestListTargetsRejectsOtherKindAndCursor(t *testing.T) {
	var calls []call
	serve(t, &calls, func(r *http.Request) (*http.Response, error) { t.Fatal("request"); return nil, nil })
	red := &redact.Redactor{}
	for _, req := range []config.TargetListRequest{{Kind: "workspace"}, {Kind: "base", Cursor: "2"}} {
		if _, err := listTargets(context.Background(), browseResolved(), resolver(red, nil), red, req); !isInvalidRequest(err) {
			t.Errorf("%+v: %v", req, err)
		}
	}
}

func TestResolveTargetURL(t *testing.T) {
	cases := []struct{ name, url, want string }{
		{"own host", "https://seatable.example.test/workspace/42/dtable/My%20Base/", "42/My Base"},
		{"no trailing slash", "https://seatable.example.test/workspace/42/dtable/Base", "42/Base"},
		{"encoded slash", "https://seatable.example.test/workspace/42/dtable/a%2Fb/", ""},
		{"foreign host", "https://evil.test/workspace/42/dtable/Base/", ""},
		{"http", "http://seatable.example.test/workspace/42/dtable/Base/", ""},
		{"missing name", "https://seatable.example.test/workspace/42/dtable/", ""},
		{"missing id", "https://seatable.example.test/workspace//dtable/Base/", ""},
		{"bad id", "https://seatable.example.test/workspace/x/dtable/Base/", ""},
		{"extra segment", "https://seatable.example.test/workspace/42/dtable/Base/tables", ""},
	}
	for _, c := range cases {
		entries, err := resolveTargetURL(context.Background(), nil, nil, nil, baseURL, c.url)
		got := ""
		if len(entries) == 1 {
			got = entries[0].Target
		}
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v, want %q", c.name, got, err, c.want)
		}
	}
}

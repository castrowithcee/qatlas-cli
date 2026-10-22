package forge

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func milestoneFixture(id string, state string) string {
	return `{"id":` + id + `,"title":"v1","description":"First","state":"` + state + `","open_issues":3,` +
		`"closed_issues":4,"due_on":"2030-01-31T00:00:00Z","created_at":"2030-01-01T00:00:00Z",` +
		`"updated_at":"2030-01-02T00:00:00Z","closed_at":null}`
}

const milestonesBase = "/api/v1/repos/acme/widget/milestones"

func milestoneServer(change func(call) *http.Response) func(call) *http.Response {
	return func(c call) *http.Response {
		switch {
		case c.path == milestonesBase && c.method == http.MethodGet:
			return jsonResponse(200, `[`+milestoneFixture("2", "open")+`]`, "X-Total-Count: 1")
		case c.path == milestonesBase+"/2" && c.method == http.MethodGet:
			return jsonResponse(200, milestoneFixture("2", "open"))
		case c.path == milestonesBase+"/3" && c.method == http.MethodGet:
			return jsonResponse(200, milestoneFixture("99", "open"))
		}
		if change != nil {
			return change(c)
		}
		return notFoundResponse()
	}
}

func TestMilestoneReadsSendFixedRequests(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, milestoneServer(nil))
		result, err := e.invoke(t, milestonesList, "repo", `{"state":"all","name":"v1"}`)
		if err != nil {
			t.Fatal(err)
		}
		rows, _ := result["milestones"].([]any)
		if len(rows) != 1 || result["has_more"] != false || result["repository"] != "acme/widget" {
			t.Errorf("%s: list = %v", p.id, result)
		}
		first, _ := rows[0].(map[string]any)
		if first["title"] != "v1" || first["open_issues"] != float64(3) || first["due_on"] == nil {
			t.Errorf("%s: row = %v", p.id, first)
		}
		c := (*e.calls)[0]
		if c.method != http.MethodGet || c.path != milestonesBase ||
			!strings.Contains(c.query, "state=all") || !strings.Contains(c.query, "name=v1") {
			t.Errorf("%s: call = %+v", p.id, c)
		}
		got, err := e.invoke(t, milestonesGet, "repo", `{"id":2}`)
		if err != nil || got["id"] != float64(2) {
			t.Errorf("%s: get = %v, %v", p.id, got, err)
		}
		// An answer with another ID than the path names is refused.
		if _, err := e.invoke(t, milestonesGet, "repo", `{"id":3}`); err == nil {
			t.Errorf("%s: a milestone with another ID was accepted", p.id)
		}
	}
}

func TestMilestoneTargetsAndArgumentsAreRefusedBeforeAnyRequest(t *testing.T) {
	cases := map[*tool][]string{
		milestonesList:   {`{"repository":"secret-owner/hidden"}`, `{"state":"bogus"}`},
		milestonesGet:    {`{"repository":"secret-owner/hidden","id":2}`, `{"id":0}`},
		milestonesCreate: {`{"repository":"secret-owner/hidden","title":"x"}`, `{}`, `{"title":"x","due_on":"soon"}`},
		milestonesUpdate: {`{"repository":"secret-owner/hidden","id":2,"title":"x"}`, `{"id":2}`, `{"id":2,"state":"all"}`},
	}
	for _, p := range products {
		for tl, list := range cases {
			for _, arguments := range list {
				e := newEnvironment(t, p, milestoneServer(nil))
				_, err := e.invoke(t, tl, "repo", arguments)
				if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") || *e.reads != 0 ||
					len(*e.calls) != 0 {
					t.Errorf("%s %s %s: %v, reads %d, calls %d", p.id, tl.action, arguments, err, *e.reads, len(*e.calls))
				}
			}
		}
		e := newEnvironment(t, p, milestoneServer(nil))
		if _, err := e.invoke(t, milestonesGet, "open", `{"id":2}`); !isInvalidRequest(err) {
			t.Errorf("%s: a repository is required without a single target: %v", p.id, err)
		}
	}
}

func TestMilestoneCreateAndUpdateSendOneRequest(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, milestoneServer(func(c call) *http.Response {
			switch {
			case c.method == http.MethodPost && c.path == milestonesBase:
				return jsonResponse(201, milestoneFixture("2", "open"))
			case c.method == http.MethodPatch && c.path == milestonesBase+"/2":
				return jsonResponse(200, milestoneFixture("2", "closed"))
			case c.method == http.MethodPatch && c.path == milestonesBase+"/3":
				return jsonResponse(200, milestoneFixture("99", "closed"))
			}
			return notFoundResponse()
		}))
		if _, err := e.invokeAs(t, milestonesCreate, "repo", `{"title":"v1"}`, false); err == nil || len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed create: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, milestonesUpdate, "repo", `{"id":2,"state":"closed"}`, false); err == nil ||
			len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed update: %v", p.id, err)
		}
		if _, err := e.invoke(t, milestonesCreate, "repo",
			`{"title":"v1","description":"First","due_on":"2030-01-31T00:00:00Z","state":"open"}`); err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		result, err := e.invoke(t, milestonesUpdate, "repo", `{"id":2,"state":"closed"}`)
		if err != nil || result["state"] != "closed" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"POST " + milestonesBase, "GET " + milestonesBase + "/2", "PATCH " + milestonesBase + "/2"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		if body := bodyOf(t, (*e.calls)[0]); body["title"] != "v1" || body["due_on"] != "2030-01-31T00:00:00Z" ||
			body["state"] != "open" || body["description"] != "First" {
			t.Errorf("%s: create body = %v", p.id, body)
		}
		if body := bodyOf(t, (*e.calls)[2]); !reflect.DeepEqual(body, map[string]any{"state": "closed"}) {
			t.Errorf("%s: update body = %v", p.id, body)
		}
		before := len(*e.calls)
		for _, id := range []string{"3", "4"} {
			if _, err := e.invoke(t, milestonesUpdate, "repo", `{"id":`+id+`,"state":"closed"}`); err == nil {
				t.Errorf("%s: milestone %s was accepted", p.id, id)
			}
		}
		for _, c := range (*e.calls)[before:] {
			if c.method != http.MethodGet {
				t.Errorf("%s: %s %s after a failed evidence read", p.id, c.method, c.path)
			}
		}
	}
}

func TestMilestoneDeleteOfAForeignIDSendsNoDelete(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, milestoneServer(func(c call) *http.Response {
			if c.method != http.MethodGet {
				t.Errorf("%s: %s %s", p.id, c.method, c.path)
			}
			return notFoundResponse()
		}))
		for _, id := range []string{"3", "4"} {
			if _, err := e.invoke(t, milestonesDelete, "delete", `{"id":`+id+`}`); err == nil {
				t.Errorf("%s: milestone %s was deleted", p.id, id)
			}
		}
		for _, c := range *e.calls {
			if c.method != http.MethodGet {
				t.Errorf("%s: %s %s", p.id, c.method, c.path)
			}
		}
	}
}

func TestMilestoneDeleteIsListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		d, _, ok := reg.Lookup(p.toolID(milestonesDelete))
		if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
			t.Fatalf("%s descriptor = %+v", p.id, d)
		}
		for _, other := range reg.Provider(p.id) {
			if strings.Contains(other.ID, ".milestones.") && other.ID != d.ID && other.RequiresToolAllowList {
				t.Errorf("%s is listed-only too", other.ID)
			}
		}
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if strings.Contains(id, ".milestones.") {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, id)
				}
			}
		}
		e := newEnvironment(t, p, milestoneServer(func(c call) *http.Response {
			if c.method == http.MethodDelete && c.path == milestonesBase+"/2" {
				return jsonResponse(204, "")
			}
			return notFoundResponse()
		}))
		if _, err := e.invoke(t, milestonesDelete, "repo", `{"id":2}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, milestonesDelete, "delete", `{"id":2}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		result, err := e.invoke(t, milestonesDelete, "delete", `{"id":2}`)
		if err != nil || result["deleted"] != true || result["id"] != float64(2) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + milestonesBase + "/2", "DELETE " + milestonesBase + "/2"}) || (*e.calls)[1].body != "" {
			t.Errorf("%s calls = %v", p.id, got)
		}
	}
}

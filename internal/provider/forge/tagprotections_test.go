package forge

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
)

const tagProtectionsAPI = "/api/v1/repos/acme/widget/tag_protections"

// The rule fixture has the answer form of the swagger files of Forgejo 15 through 17 and Gitea 1.27 and 28.
const tagProtectionFixture = `{"id":3,"name_pattern":"v*","whitelist_usernames":["octo"],"whitelist_teams":["core"],` +
	`"created_at":"2030-01-01T00:00:00Z","updated_at":"2030-01-02T00:00:00Z"}`

// tagProtectionServer answers the routes with the swagger shapes; status is the answer of every change.
func tagProtectionServer(status *int) func(call) *http.Response {
	return func(c call) *http.Response {
		switch {
		case c.method != http.MethodGet:
			if *status == 201 || *status == 200 {
				return jsonResponse(*status, tagProtectionFixture)
			}
			return jsonResponse(*status, "")
		case c.path == tagProtectionsAPI:
			return jsonResponse(200, `[`+tagProtectionFixture+`,null]`)
		case c.path == tagProtectionsAPI+"/3":
			return jsonResponse(200, tagProtectionFixture)
		}
		return notFoundResponse()
	}
}

var tagProtectionTools = []*tool{tagProtectionsList, tagProtectionsGet, tagProtectionsCreate, tagProtectionsUpdate,
	tagProtectionsDelete}

func TestTagProtectionsListAndGetShowEveryMember(t *testing.T) {
	for _, p := range products {
		status := 204
		e := newEnvironment(t, p, tagProtectionServer(&status))
		result, err := e.invoke(t, tagProtectionsList, "repo", `{}`)
		rules := rowsOf(t, result, "tag_protections")
		if err != nil || len(rules) != 1 || result["repository"] != "acme/widget" || result["truncated"] != nil {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + tagProtectionsAPI}) || (*e.calls)[0].query != "" {
			t.Errorf("%s calls = %v %q", p.id, got, (*e.calls)[0].query)
		}
		one, err := e.invoke(t, tagProtectionsGet, "repo", `{"id":3}`)
		if err != nil || one["repository"] != "acme/widget" || one["name_pattern"] != "v*" {
			t.Fatalf("%s: %v, %v", p.id, one, err)
		}
		for _, f := range e.descriptor(t, tagProtectionsGet).Fields {
			if _, ok := one[f.Name]; !ok {
				t.Errorf("%s: documented member %s is missing from the answer", p.id, f.Name)
			}
		}
	}
}

func TestTagProtectionsListCapsAtOneHundredAndSaysSo(t *testing.T) {
	for _, p := range products {
		rows := strings.TrimSuffix(strings.Repeat(tagProtectionFixture+",", 101), ",")
		e := newEnvironment(t, p, func(c call) *http.Response { return jsonResponse(200, `[`+rows+`]`) })
		result, err := e.invoke(t, tagProtectionsList, "repo", `{}`)
		if err != nil || len(rowsOf(t, result, "tag_protections")) != 100 || result["truncated"] != true {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}

func TestTagProtectionsAreBoundToTheTargetsBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		status := 204
		for _, tl := range tagProtectionTools {
			for _, connection := range []string{"repo", "exclude"} {
				repository := "other/tool"
				if connection == "exclude" {
					repository = "acme/gadget"
				}
				if tl == tagProtectionsDelete && connection == "repo" {
					connection = "tag-protection-delete"
				}
				e := newEnvironment(t, p, tagProtectionServer(&status))
				_, err := e.invoke(t, tl, connection,
					`{"repository":"`+repository+`","id":3,"name_pattern":"v*","whitelist_teams":["core"]}`)
				refused(t, e, tl.action+" "+connection, err)
				if err != nil && strings.Contains(err.Error(), repository) {
					t.Errorf("%s: the refusal names the repository: %v", p.id, err)
				}
			}
		}
	}
}

func TestTagProtectionIDsMustBePositiveIntegers(t *testing.T) {
	for _, p := range products {
		status := 204
		for _, tl := range []*tool{tagProtectionsGet, tagProtectionsUpdate, tagProtectionsDelete} {
			for _, id := range []string{`0`, `-1`, `1.5`, `"3"`, `"../3"`, `null`, `true`} {
				e := newEnvironment(t, p, tagProtectionServer(&status))
				connection, args := "repo", `{"id":`+id+`,"name_pattern":"v*"}`
				if tl == tagProtectionsDelete {
					connection, args = "tag-protection-delete", `{"id":`+id+`}`
				}
				_, err := e.invoke(t, tl, connection, args)
				refused(t, e, tl.action+" id "+id, err)
			}
			e := newEnvironment(t, p, tagProtectionServer(&status))
			connection := "repo"
			if tl == tagProtectionsDelete {
				connection = "tag-protection-delete"
			}
			_, err := e.invoke(t, tl, connection, `{}`)
			refused(t, e, tl.action+" without an id", err)
		}
	}
}

func TestTagProtectionMembersAreCheckedAndNoProductOnlyMemberIsOffered(t *testing.T) {
	long := strings.Repeat("a", 256)
	many := `[` + strings.TrimSuffix(strings.Repeat(`"a",`, 101), ",") + `]`
	for _, p := range products {
		status := 201
		for _, tl := range []*tool{tagProtectionsCreate, tagProtectionsUpdate} {
			for _, members := range []string{`"name_pattern":""`, `"name_pattern":"` + long + `"`, `"name_pattern":"a\u0001"`,
				`"name_pattern":"a\nb"`, `"name_pattern":5`, `"name_pattern":null`, `"whitelist_usernames":"octo"`,
				`"whitelist_usernames":["a/b"]`, `"whitelist_usernames":[""]`, `"whitelist_usernames":null`,
				`"whitelist_usernames":` + many, `"whitelist_teams":["a\u0001"]`, `"whitelist_teams":[""]`,
				`"whitelist_teams":[1]`, `"rule_name":"v*"`, `"apply_to_admins":true`, `"priority":1`, `"unknown":true`} {
				e := newEnvironment(t, p, tagProtectionServer(&status))
				_, err := e.invoke(t, tl, "repo", `{"id":3,`+withBase(members)+`}`)
				refused(t, e, tl.action+" "+members, err)
			}
			schema := string(newEnvironment(t, p, tagProtectionServer(&status)).descriptor(t, tl).InputSchema)
			for _, name := range []string{`"name_pattern"`, `"whitelist_usernames"`, `"whitelist_teams"`} {
				if !strings.Contains(schema, name) {
					t.Errorf("%s %s schema lacks %s", p.id, tl.action, name)
				}
			}
		}
		e := newEnvironment(t, p, tagProtectionServer(&status))
		_, err := e.invoke(t, tagProtectionsCreate, "repo", `{"whitelist_teams":["core"]}`)
		refused(t, e, "create without a pattern", err)
	}
}

func TestTagProtectionCreateSendsOneRequestWithOrWithoutAllowlists(t *testing.T) {
	for _, p := range products {
		status := 201
		e := newEnvironment(t, p, tagProtectionServer(&status))
		args := `{"name_pattern":"v*","whitelist_usernames":["octo"],"whitelist_teams":[]}`
		if _, err := e.invokeAs(t, tagProtectionsCreate, "repo", args, false); err == nil || len(*e.calls) != 0 ||
			*e.reads != 0 {
			t.Fatalf("%s: unconfirmed create: %v", p.id, err)
		}
		result, err := e.invoke(t, tagProtectionsCreate, "repo", args)
		if err != nil || !isThree(result["id"]) || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"POST " + tagProtectionsAPI}) ||
			(*e.calls)[0].body != `{"name_pattern":"v*","whitelist_teams":[],"whitelist_usernames":["octo"]}` {
			t.Errorf("%s calls = %v body %q", p.id, got, (*e.calls)[0].body)
		}
		e = newEnvironment(t, p, tagProtectionServer(&status))
		if _, err := e.invoke(t, tagProtectionsCreate, "repo", `{"name_pattern":"release-*"}`); err != nil ||
			(*e.calls)[0].body != `{"name_pattern":"release-*"}` {
			t.Errorf("%s without allowlists: %v %v", p.id, err, *e.calls)
		}
	}
}

func TestTagProtectionUpdateSendsOnlyTheGivenMembers(t *testing.T) {
	for _, p := range products {
		status := 200
		e := newEnvironment(t, p, tagProtectionServer(&status))
		if _, err := e.invokeAs(t, tagProtectionsUpdate, "repo", `{"id":3,"name_pattern":"v1*"}`, false); err == nil ||
			len(*e.calls) != 0 || *e.reads != 0 {
			t.Fatalf("%s: unconfirmed update: %v", p.id, err)
		}
		_, err := e.invoke(t, tagProtectionsUpdate, "repo", `{"id":3}`)
		refused(t, e, "update without members", err)
		result, err := e.invoke(t, tagProtectionsUpdate, "repo", `{"id":3,"whitelist_teams":["core"]}`)
		if err != nil || !isThree(result["id"]) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"PATCH " + tagProtectionsAPI + "/3"}) ||
			(*e.calls)[0].body != `{"whitelist_teams":["core"]}` {
			t.Errorf("%s calls = %v body %q", p.id, got, (*e.calls)[0].body)
		}
	}
}

func TestTagProtectionDeleteIsListedOnly(t *testing.T) {
	for _, p := range products {
		status := 204
		e := newEnvironment(t, p, tagProtectionServer(&status))
		if _, err := e.invoke(t, tagProtectionsDelete, "repo", `{"id":3}`); err == nil || len(*e.calls) != 0 {
			t.Fatalf("%s: delete without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, tagProtectionsDelete, "tag-protection-delete", `{"id":3}`, false); err == nil ||
			len(*e.calls) != 0 || *e.reads != 0 {
			t.Fatalf("%s: unconfirmed delete: %v", p.id, err)
		}
		result, err := e.invoke(t, tagProtectionsDelete, "tag-protection-delete", `{"id":3}`)
		if err != nil || result["deleted"] != true || !isThree(result["id"]) || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"DELETE " + tagProtectionsAPI + "/3"}) ||
			(*e.calls)[0].body != "" {
			t.Errorf("%s calls = %v body %q", p.id, got, (*e.calls)[0].body)
		}
	}
}

func TestTagProtectionChangesAreNeverRepeatedAfterAnUnclearResult(t *testing.T) {
	for _, p := range products {
		for _, tl := range tagProtectionTools[2:] {
			args, connection := `{"id":3,"name_pattern":"v*"}`, "repo"
			if tl == tagProtectionsCreate {
				args = `{"name_pattern":"v*"}`
			}
			if tl == tagProtectionsDelete {
				args, connection = `{"id":3}`, "tag-protection-delete"
			}
			for _, status := range []int{502, 500} {
				e := newEnvironment(t, p, func(c call) *http.Response { return jsonResponse(status, bodyCanary) })
				_, err := e.invoke(t, tl, connection, args)
				if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
					t.Errorf("%s %s %d: %v after %d calls", p.id, tl.action, status, err, len(*e.calls))
				}
			}
		}
	}
}

func TestTagProtectionToolsRisk(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range tagProtectionTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok {
				t.Fatalf("%s is not registered", p.toolID(tl))
			}
			change := tl != tagProtectionsList && tl != tagProtectionsGet
			effect := map[*tool]capability.Effect{tagProtectionsList: capability.EffectRead,
				tagProtectionsGet: capability.EffectRead, tagProtectionsCreate: capability.EffectCreate,
				tagProtectionsUpdate: capability.EffectUpdate, tagProtectionsDelete: capability.EffectDelete}[tl]
			if d.Risk.Effect != effect || d.Risk.Idempotency == "" || !d.Risk.OpenWorld ||
				d.Risk.DataSensitivity != p.id+"-access-data" ||
				(change != (d.Risk.Confirmation == capability.ConfirmationRequired)) ||
				d.RequiresToolAllowList != (tl == tagProtectionsDelete) {
				t.Errorf("%s risk = %+v listed-only %t", d.ID, d.Risk, d.RequiresToolAllowList)
			}
			if (tl == tagProtectionsCreate) != (d.Risk.Idempotency == capability.IdempotencyNonIdempotent) && change {
				t.Errorf("%s idempotency = %s", d.ID, d.Risk.Idempotency)
			}
			for _, profile := range metadata.Profiles {
				if slices.Contains(profile.Tools, d.ID) && change {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
				}
			}
		}
	}
}

// withBase adds a valid name_pattern unless the members set one, so each case differs in one member only.
func withBase(members string) string {
	if strings.HasPrefix(members, `"name_pattern"`) {
		return members
	}
	return `"name_pattern":"v*",` + members
}

func isThree(v any) bool { return fmt.Sprint(v) == "3" }

package forge

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

var adminBadgeTools = []*tool{adminUserBadgesList, adminUserBadgesAdd, adminUserBadgesDelete}

const adminBadgeFixture = `{"id":3,"slug":"early","description":"Early","image_url":"https://example.test/b.png"}`

func badgeArguments(tl *tool) string {
	if tl == adminUserBadgesList {
		return `{"username":"octo"}`
	}
	return `{"username":"octo","badge_slugs":["early"]}`
}

func TestAdminUserBadgesRegisterOnlyOnGiteaWithTheirRisk(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, tl := range adminBadgeTools {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if ok != (p == gitea) {
				t.Errorf("%s registered = %t", p.toolID(tl), ok)
			}
			if !ok {
				continue
			}
			want := map[*tool]capability.Effect{adminUserBadgesList: capability.EffectRead,
				adminUserBadgesAdd: capability.EffectCreate, adminUserBadgesDelete: capability.EffectDelete}[tl]
			if d.Risk.Effect != want || d.RequiresToolAllowList != (tl == adminUserBadgesDelete) ||
				!strings.HasSuffix(d.Risk.DataSensitivity, adminAccountsSensitivity) ||
				(tl != adminUserBadgesList && d.Risk.Confirmation != capability.ConfirmationRequired) {
				t.Errorf("%s descriptor = %+v", d.ID, d)
			}
			for _, profile := range metadata.Profiles {
				if slices.Contains(profile.Tools, d.ID) {
					t.Errorf("profile %s holds %s", profile.ID, d.ID)
				}
			}
		}
	}
}

func TestAdminUserBadgesRefuseTargetsBeforeAnyRequest(t *testing.T) {
	e := newAdminEnvironment(t, gitea, defaultVersion(gitea), func(call) *http.Response { return jsonResponse(200, `[]`) })
	for _, tl := range adminBadgeTools {
		for _, connection := range []string{"admin-repo", "admin-org", "admin-user"} {
			reads := *e.reads
			_, err := e.call(t, tl, connection, badgeArguments(tl), true)
			if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != reads ||
				(err != nil && strings.Contains(err.Error(), "alice")) {
				t.Errorf("%s on %s: %v", tl.action, connection, err)
			}
		}
	}
}

func TestAdminUserBadgesListIsUnpagedCappedAndDropsRiskyAddresses(t *testing.T) {
	for _, v := range []string{"1.27.3", "28.2.0"} {
		e := newAdminEnvironment(t, gitea, v, func(call) *http.Response {
			rows := make([]string, maxRunnerRows+5)
			for i := range rows {
				rows[i] = adminBadgeFixture
			}
			rows[0] = `{"id":4,"slug":"x","image_url":"https://u:p@example.test/b.png?token=1"}`
			return jsonResponse(200, `[`+strings.Join(rows, ",")+`]`)
		})
		result, err := e.call(t, adminUserBadgesList, "admin-open", `{"username":"Octo"}`, false)
		rows := rowsOf(t, result, "badges")
		c := apiCalls(e.environment)
		if err != nil || len(rows) != maxRunnerRows || result["has_more"] != true || len(c) != 1 ||
			c[0].method != "GET" || c[0].path != "/api/v1/admin/users/Octo/badges" || c[0].query != "" {
			t.Fatalf("%s list: %d rows, %v, %+v", v, len(rows), err, c)
		}
		if _, has := rows[0]["image_url"]; has || rows[0]["slug"] != "x" || rows[1]["image_url"] == nil {
			t.Errorf("%s projection = %v", v, rows[:2])
		}
		for _, arguments := range []string{`{}`, `{"username":"a/b"}`, `{"username":"octo","limit":5}`} {
			before := len(*e.calls)
			if _, err := e.call(t, adminUserBadgesList, "admin-open", arguments, false); err == nil || len(*e.calls) != before {
				t.Errorf("list %s: %v", arguments, err)
			}
		}
	}
}

func TestAdminUserBadgesChangeSendOneBodyAndAreNeverRepeated(t *testing.T) {
	var unsupported *capability.UnsupportedError
	l := newEnvironment(t, gitea, instance(defaultVersion(gitea), defaultVersion(gitea), nil))
	if _, err := l.invoke(t, adminUserBadgesDelete, "open", badgeArguments(adminUserBadgesDelete)); !errors.As(err, &unsupported) ||
		len(*l.calls) != 0 {
		t.Errorf("delete without a tools list: %v", err)
	}
	for _, v := range []string{"1.27.3", "28.2.0"} {
		for _, tl := range []*tool{adminUserBadgesAdd, adminUserBadgesDelete} {
			method := map[*tool]string{adminUserBadgesAdd: "POST", adminUserBadgesDelete: "DELETE"}[tl]
			key := map[*tool]string{adminUserBadgesAdd: "added", adminUserBadgesDelete: "deleted"}[tl]
			f := newAdminEnvironment(t, gitea, v, func(call) *http.Response { return jsonResponse(204, ``) })
			var confirm *application.ConfirmationRequiredError
			if _, err := f.call(t, tl, "admin-open", badgeArguments(tl), false); !errors.As(err, &confirm) || len(*f.calls) != 0 {
				t.Errorf("%s unconfirmed: %v", tl.action, err)
			}
			result, err := f.call(t, tl, "admin-open", `{"username":"octo","badge_slugs":["a-1","b_2.x"]}`, true)
			c := apiCalls(f.environment)
			if err != nil || result[key] != true || result["count"] != float64(2) || len(c) != 1 || c[0].method != method ||
				c[0].path != "/api/v1/admin/users/octo/badges" || c[0].query != "" ||
				c[0].body != `{"badge_slugs":["a-1","b_2.x"]}` {
				t.Fatalf("%s %s: %v, %v, %+v", v, tl.action, result, err, c)
			}
			tooMany := make([]string, maxBadgeSlugs+1)
			for i := range tooMany {
				tooMany[i] = `"s` + strconv.Itoa(i) + `"`
			}
			for _, arguments := range []string{`{"username":"octo"}`, `{"username":"octo","badge_slugs":[]}`,
				`{"username":"a/b","badge_slugs":["a"]}`, `{"username":"octo","badge_slugs":["A"]}`,
				`{"username":"octo","badge_slugs":["a b"]}`, `{"username":"octo","badge_slugs":[""]}`,
				`{"username":"octo","badge_slugs":["a","a"]}`,
				`{"username":"octo","badge_slugs":["` + strings.Repeat("a", maxBadgeSlug+1) + `"]}`,
				`{"username":"octo","badge_slugs":[` + strings.Join(tooMany, ",") + `]}`,
				`{"username":"octo","badge_slugs":["a"],"extra":1}`, `{"username":"octo","badge_slugs":["a"],"sudo":"root"}`} {
				before := len(*f.calls)
				if _, err := f.call(t, tl, "admin-open", arguments, true); err == nil || len(*f.calls) != before {
					t.Errorf("%s %s: %v", tl.action, arguments, err)
				}
			}
			g := newAdminEnvironment(t, gitea, v, func(call) *http.Response {
				return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
			})
			_, err = g.call(t, tl, "admin-open", badgeArguments(tl), true)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), bodyCanary) ||
				len(apiCalls(g.environment)) != 1 {
				t.Errorf("%s after 500: %v", tl.action, err)
			}
		}
	}
}

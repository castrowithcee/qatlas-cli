package makeapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func teamBody(team, org int64) string {
	return `{"team":{"id":` + strconv.FormatInt(team, 10) + `,"name":"Own","organizationId":` +
		strconv.FormatInt(org, 10) + `,"operationsLimit":1000,"consumedOperations":5,"secret":"x"}}`
}

func teamsHandler(t *testing.T, org int64, orgBody string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case apiPath + "/teams/" + strconv.FormatInt(ownTeam, 10):
			return jsonResponse(200, teamBody(ownTeam, org)), nil
		case apiPath + "/organizations/" + strconv.FormatInt(org, 10):
			return jsonResponse(200, orgBody), nil
		case apiPath + "/teams/" + strconv.FormatInt(ownTeam, 10) + "/usage":
			return jsonResponse(200, `{"data":[{"date":"2026-10-01","operations":3,"dataTransfer":4,"centicredits":5}]}`), nil
		case apiPath + "/teams/" + strconv.FormatInt(ownTeam, 10) + "/user-team-roles":
			return jsonResponse(200, `{"userTeamRoles":[{"userId":7,"teamId":1001,"usersRoleId":2,"changeable":true,"email":"a@b.c"},`+
				`{"userId":8,"teamId":2002,"usersRoleId":2}]}`), nil
		}
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	}
}

func TestTeamReadsUseBoundTeamAndNarrowFields(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, teamsHandler(t, ownOrg, `{}`))
	got, err := env.invoke(teamGet.ID, "open", `{}`)
	if err != nil || !strings.Contains(got, `"operations_limit":1000`) || strings.Contains(got, "secret") {
		t.Fatalf("get = %s, %v", got, err)
	}
	got, err = env.invoke(teamUsage.ID, "open", `{}`)
	if err != nil || !strings.Contains(got, `"data_transfer":4`) {
		t.Fatalf("usage = %s, %v", got, err)
	}
	got, err = env.invoke(teamMembers.ID, "open", `{}`)
	if err != nil || !strings.Contains(got, `"user_id":7`) || strings.Contains(got, `"user_id":8`) ||
		strings.Contains(got, "a@b.c") {
		t.Fatalf("members = %s, %v", got, err)
	}
	if _, err = env.invoke(teamGet.ID, "open", `{"team_id":2002}`); err == nil {
		t.Fatal("a team argument must be refused")
	}
}

func TestOrganizationGetLimitsFields(t *testing.T) {
	var calls []call
	body := `{"organization":{"id":501,"name":"N","zone":"eu1.make.com","teams":[{"id":9}],"ssoType":"saml2",` +
		`"license":{"operations":10000,"dlm":true,"plan":"pro","nested":{"a":1},"list":[1]}}}`
	env := newEnvironment(t, &calls, teamsHandler(t, ownOrg, body))
	got, err := env.invoke(organizationGet.ID, "org", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"teams", "ssoType", "nested", `"list"`, `"name"`} {
		if strings.Contains(got, bad) {
			t.Fatalf("result %s leaks %s", got, bad)
		}
	}
	if !strings.Contains(got, `"operations":10000`) || !strings.Contains(got, `"zone":"eu1.make.com"`) {
		t.Fatalf("result = %s", got)
	}
}

func TestOrganizationTargetMismatchIsRefusedWithoutForeignID(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, teamsHandler(t, foreignOrg, `{}`))
	_, err := env.invoke(organizationGet.ID, "org", `{}`)
	if !isInvalidRequest(err) {
		t.Fatalf("err = %v, want invalid request", err)
	}
	if strings.Contains(err.Error(), strconv.FormatInt(foreignOrg, 10)) || len(calls) != 1 {
		t.Fatalf("err = %v, calls = %d", err, len(calls))
	}
}

func TestTeamReadForbiddenNamesScope(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/organizations/") {
			return jsonResponse(403, `{}`), nil
		}
		if strings.HasSuffix(r.URL.Path, "/usage") {
			return jsonResponse(403, `{}`), nil
		}
		return jsonResponse(200, teamBody(ownTeam, ownOrg)), nil
	})
	for op, want := range map[string]string{teamUsage.ID: "teams:read", organizationGet.ID: "organizations:read"} {
		_, err := env.invoke(op, "open", `{}`)
		var perr *provider.Error
		if !errors.As(err, &perr) || perr.Class != provider.ClassPermission || !strings.Contains(perr.Message, want) {
			t.Fatalf("%s err = %v, want %s hint", op, err, want)
		}
	}
}

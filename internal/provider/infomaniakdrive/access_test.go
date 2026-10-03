package infomaniakdrive

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The addresses are synthetic, on the reserved .invalid domain, and assembled from parts.
const (
	inviteeLocal = "invitee"
	secondLocal  = "second"
	testDomain   = "example.invalid"

	memberUser  int64 = 7
	memberUser2 int64 = 8
	memberTeam  int64 = 3
	foreignUser int64 = 777
	foreignTeam int64 = 333
)

var (
	inviteeEmail = inviteeLocal + "@" + testDomain
	secondEmail  = secondLocal + "@" + testDomain
)

func accessPathOf(fileID int64) string {
	return fmt.Sprintf("/2/drive/%d/files/%d/access", ownDrive, fileID)
}

func usersPathOf() string { return fmt.Sprintf("/2/drive/%d/users", ownDrive) }

// memberJSONOf is one drive user as the users endpoint answers it.
func memberJSONOf(id int64, status string, teams ...int64) string {
	list, _ := json.Marshal(teams)
	if teams == nil {
		list = []byte("[]")
	}
	return fmt.Sprintf(`{"id":%d,"drive_id":%d,"status":%q,"deleted_at":0,"role":"user","teams":%s}`,
		id, ownDrive, status, list)
}

// usersPage answers one page of the users endpoint with its page counters as siblings of data.
func usersPage(page, pages int, members ...string) string {
	return fmt.Sprintf(`{"result":"success","data":[%s],"page":%d,"pages":%d,"total":%d}`,
		strings.Join(members, ","), page, pages, len(members))
}

// membership answers the users endpoint with the same one page.
func membership(next func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == usersPathOf() {
			return jsonResponse(200, usersPage(1, 1, memberJSONOf(memberUser, "active", memberTeam),
				memberJSONOf(memberUser2, "active"), memberJSONOf(foreignUser, "locked", foreignTeam))), nil
		}
		return next(r)
	})
}

func accessArgs(extra string) string {
	return fmt.Sprintf(`{"drive_id":%d,"file_id":%d%s}`, ownDrive, childFileID, extra)
}

func grantFeedback() string {
	return fmt.Sprintf(`{"users":[{"id":%d,"result":true,"message":"","access":null}],`+
		`"teams":[{"id":%d,"result":true,"message":"","access":null}],`+
		`"emails":[{"id":%q,"result":true,"message":"","access":null}]}`, memberUser, memberTeam, inviteeEmail)
}

type accessCase struct {
	name, tool, args, method, path, query, body, success string
}

func accessCases() []accessCase {
	grantArgs := accessArgs(fmt.Sprintf(`,"right":"write","lang":"fr","user_ids":[%d],"team_ids":[%d],"emails":[%q]`,
		memberUser, memberTeam, inviteeEmail))
	return []accessCase{
		{"grant", accessGrant.ID, grantArgs, http.MethodPost, accessPathOf(childFileID), "lang=fr",
			fmt.Sprintf(`{"right":"write","user_ids":[%d],"team_ids":[%d],"emails":[%q]}`, memberUser, memberTeam,
				inviteeEmail), envelopeSuccess(grantFeedback())},
		{"update user", accessUpdate.ID, accessArgs(fmt.Sprintf(`,"user_id":%d,"right":"manage"`, memberUser)),
			http.MethodPut, fmt.Sprintf("%s/users/%d", accessPathOf(childFileID), memberUser), "", `{"right":"manage"}`,
			envelopeSuccess(`true`)},
		{"update team", accessUpdate.ID, accessArgs(fmt.Sprintf(`,"team_id":%d,"right":"read"`, memberTeam)),
			http.MethodPut, fmt.Sprintf("%s/teams/%d", accessPathOf(childFileID), memberTeam), "", `{"right":"read"}`,
			envelopeSuccess(`true`)},
		{"revoke user", accessRevoke.ID, accessArgs(fmt.Sprintf(`,"user_id":%d`, memberUser)), http.MethodDelete,
			fmt.Sprintf("%s/users/%d", accessPathOf(childFileID), memberUser), "", "", envelopeSuccess(`true`)},
		{"revoke team", accessRevoke.ID, accessArgs(fmt.Sprintf(`,"team_id":%d`, memberTeam)), http.MethodDelete,
			fmt.Sprintf("%s/teams/%d", accessPathOf(childFileID), memberTeam), "", "", envelopeSuccess(`true`)},
	}
}

func TestAccessDescriptors(t *testing.T) {
	want := map[string]capability.Effect{accessGet.ID: capability.EffectRead, accessGrant.ID: capability.EffectCreate,
		accessUpdate.ID: capability.EffectUpdate, accessRevoke.ID: capability.EffectDelete}
	for _, d := range []capability.Descriptor{accessGet, accessGrant, accessUpdate, accessRevoke} {
		r := d.Risk
		read := d.ID == accessGet.ID
		if r.Effect != want[d.ID] || !d.RequiresToolAllowList || !r.OpenWorld || r.DataSensitivity != accessSensitivity ||
			r.DataSensitivity == dataSensitivity || r.DataSensitivity == linkSensitivity {
			t.Errorf("%s = %+v list=%v", d.ID, r, d.RequiresToolAllowList)
		}
		if read && (r.Confirmation != capability.ConfirmationNone || r.Idempotency != capability.IdempotencySafe) ||
			!read && r.Confirmation != capability.ConfirmationRequired {
			t.Errorf("%s confirmation = %s, idempotency = %s", d.ID, r.Confirmation, r.Idempotency)
		}
	}
	if accessGrant.Risk.Idempotency != capability.IdempotencyNonIdempotent ||
		accessUpdate.Risk.Idempotency != capability.IdempotencyIdempotent ||
		accessRevoke.Risk.Idempotency != capability.IdempotencyIdempotent {
		t.Errorf("idempotency of the changes is wrong")
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if strings.HasPrefix(id, Provider+".access.") {
				t.Errorf("profile %s contains %s", profile.ID, id)
			}
		}
	}
}

func TestAccessChangesSendExactlyOneRequestAfterTheChecks(t *testing.T) {
	for _, tt := range accessCases() {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, membership(func(r *http.Request) (*http.Response, error) {
				return jsonResponse(200, tt.success), nil
			}), nil)
			result, err := env.invokeConfirmed(tt.tool, "access", tt.args)
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			if len(calls) != 3 || calls[0].path != ownershipPath(ownDrive) || calls[1].path != usersPathOf() ||
				calls[1].method != http.MethodGet {
				t.Fatalf("calls = %+v, want the ownership check, the membership read, and one change", calls)
			}
			got := calls[2]
			if got.method != tt.method || got.path != tt.path || got.query.Encode() != tt.query {
				t.Fatalf("call = %+v, want %s %s ?%s", got, tt.method, tt.path, tt.query)
			}
			if tt.body != "" && !jsonEqual(got.body, tt.body) || tt.body == "" && got.body != "" {
				t.Fatalf("body = %q, want %q", got.body, tt.body)
			}
			var out AccessChange
			if err := json.Unmarshal([]byte(result), &out); err != nil || out.Status != statusDone ||
				out.DriveID != ownDrive || out.FileID != childFileID {
				t.Fatalf("result = %s, %v", result, err)
			}
			if tt.name == "grant" && (out.Granted == nil || *out.Granted != 3 || *out.Failed != 0 || len(out.Results) != 3) {
				t.Fatalf("result = %s, want three reached targets", result)
			}
		})
	}
}

func TestAccessGrantOfOnlyEmailsSkipsTheMembershipRead(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, membership(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, envelopeSuccess(fmt.Sprintf(`{"emails":[{"id":%q,"result":true}]}`, inviteeEmail))), nil
	}), nil)
	_, err := env.invokeConfirmed(accessGrant.ID, "access",
		accessArgs(fmt.Sprintf(`,"right":"read","lang":"de","emails":[%q]`, inviteeEmail)))
	if err != nil || len(calls) != 2 || calls[1].method != http.MethodPost || strings.Contains(calls[1].body, "user_ids") {
		t.Fatalf("err = %v, calls = %+v, want the ownership check and the grant", err, calls)
	}
}

func TestAccessGrantReportsPartialAndEchoesBoundedTargets(t *testing.T) {
	long := strings.Repeat("a", 400) + "@" + testDomain
	var calls []call
	env := newEnvironment(t, &calls, membership(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, envelopeSuccess(fmt.Sprintf(`{"emails":[{"id":%q,"result":false,"message":%q},`+
			`{"id":%q,"result":true}]}`, long, foreignCanary, secondEmail))), nil
	}), nil)
	result, err := env.invokeConfirmed(accessGrant.ID, "access",
		accessArgs(fmt.Sprintf(`,"right":"read","lang":"en","emails":[%q,%q]`, inviteeEmail, secondEmail)))
	var out AccessChange
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.Status != statusPartial || *out.Granted != 1 ||
		*out.Failed != 1 || len(out.Results) != 2 || len(out.Results[0].Target) > maxEmailBytes ||
		strings.Contains(result, foreignCanary) {
		t.Fatalf("result = %s, %v", result, err)
	}
}

func TestAccessChangesNeedConfirmationAndToolList(t *testing.T) {
	cases := append(accessCases(), accessCase{name: "get", tool: accessGet.ID, args: accessArgs("")})
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			if tt.name != "get" {
				if _, err := env.invoke(tt.tool, "access", tt.args); err == nil {
					t.Fatal("an unconfirmed change ran")
				}
			}
			for _, connection := range []string{"accessunlisted", "links", "drive", "readonly", "trash"} {
				if _, err := env.invokeConfirmed(tt.tool, connection, tt.args); err == nil {
					t.Fatalf("connection %s ran %s", connection, tt.name)
				}
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

func TestAccessToolsRefuseForeignDrives(t *testing.T) {
	cases := append(accessCases(), accessCase{name: "get", tool: accessGet.ID, args: accessArgs("")})
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			foreign := strings.Replace(tt.args, fmt.Sprintf(`"drive_id":%d`, ownDrive),
				fmt.Sprintf(`"drive_id":%d`, foreignDrive), 1)
			_, err := env.invokeConfirmed(tt.tool, "access", foreign)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), fmt.Sprint(foreignDrive)) {
				t.Fatalf("err = %v, want an invalid request without the foreign value", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
			// An allow-list naming a drive of another account is caught by the live check, before anything else.
			calls = nil
			env = newEnvironment(t, &calls, withOwnership(foreignDrive, otherAccount, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("request %s reached a drive of another account", r.URL.Path)
				return nil, nil
			}), nil)
			_, err = env.invokeConfirmed(tt.tool, "accessforeign", foreign)
			if !isInvalidRequest(err) || len(calls) != 1 || calls[0].method != http.MethodGet {
				t.Fatalf("err = %v, calls = %+v, want only the ownership check and a refusal", err, calls)
			}
		})
	}
}

func TestAccessToolsValidateArguments(t *testing.T) {
	long := strings.Repeat("a", 70) + "@" + testDomain
	cases := []struct{ name, tool, args string }{
		{"root get", accessGet.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1}`, ownDrive)},
		{"root grant", accessGrant.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1,"right":"read","lang":"en","user_ids":[7]}`, ownDrive)},
		{"root update", accessUpdate.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1,"user_id":7,"right":"read"}`, ownDrive)},
		{"root revoke", accessRevoke.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1,"user_id":7}`, ownDrive)},
		{"zero file", accessGet.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":0}`, ownDrive)},
		{"string file", accessGet.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":"5/../9"}`, ownDrive)},
		{"huge file", accessGet.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":9999999999999999999}`, ownDrive)},
		{"grant no target", accessGrant.ID, accessArgs(`,"right":"read","lang":"en"`)},
		{"grant empty lists", accessGrant.ID, accessArgs(`,"right":"read","lang":"en","user_ids":[],"emails":[]`)},
		{"grant no right", accessGrant.ID, accessArgs(`,"lang":"en","user_ids":[7]`)},
		{"grant none right", accessGrant.ID, accessArgs(`,"right":"none","lang":"en","user_ids":[7]`)},
		{"grant no lang", accessGrant.ID, accessArgs(`,"right":"read","user_ids":[7]`)},
		{"grant odd lang", accessGrant.ID, accessArgs(`,"right":"read","lang":"xx","user_ids":[7]`)},
		{"grant duplicate user", accessGrant.ID, accessArgs(`,"right":"read","lang":"en","user_ids":[7,7]`)},
		{"grant negative user", accessGrant.ID, accessArgs(`,"right":"read","lang":"en","user_ids":[-7]`)},
		{"grant too many users", accessGrant.ID, accessArgs(`,"right":"read","lang":"en","user_ids":[` +
			strings.TrimSuffix(strings.Repeat("7,", 21), ",") + `]`)},
		{"grant message", accessGrant.ID, accessArgs(`,"right":"read","lang":"en","user_ids":[7],"message":"hi"`)},
		{"grant plain text email", accessGrant.ID, accessArgs(`,"right":"read","lang":"en","emails":["not-an-address"]`)},
		{"grant two at signs", accessGrant.ID, accessArgs(fmt.Sprintf(`,"right":"read","lang":"en","emails":[%q]`,
			inviteeLocal+"@@"+testDomain))},
		{"grant spaced email", accessGrant.ID, accessArgs(fmt.Sprintf(`,"right":"read","lang":"en","emails":[%q]`,
			inviteeLocal+" x@"+testDomain))},
		{"grant control email", accessGrant.ID, accessArgs(fmt.Sprintf(`,"right":"read","lang":"en","emails":[%q]`,
			inviteeLocal+"\n@"+testDomain))},
		{"grant dotless domain", accessGrant.ID, accessArgs(fmt.Sprintf(`,"right":"read","lang":"en","emails":[%q]`,
			inviteeLocal+"@invalid"))},
		{"grant long local part", accessGrant.ID, accessArgs(fmt.Sprintf(`,"right":"read","lang":"en","emails":[%q]`, long))},
		{"grant duplicate email", accessGrant.ID, accessArgs(fmt.Sprintf(`,"right":"read","lang":"en","emails":[%q,%q]`,
			inviteeEmail, strings.ToUpper(inviteeEmail)))},
		{"grant too many emails", accessGrant.ID, accessArgs(`,"right":"read","lang":"en","emails":[` +
			strings.TrimSuffix(strings.Repeat(fmt.Sprintf(`%q,`, inviteeEmail), 11), ",") + `]`)},
		{"update both targets", accessUpdate.ID, accessArgs(`,"user_id":7,"team_id":3,"right":"read"`)},
		{"update no target", accessUpdate.ID, accessArgs(`,"right":"read"`)},
		{"update no right", accessUpdate.ID, accessArgs(`,"user_id":7`)},
		{"update none right", accessUpdate.ID, accessArgs(`,"user_id":7,"right":"none"`)},
		{"update string user", accessUpdate.ID, accessArgs(`,"user_id":"7/../8","right":"read"`)},
		{"revoke both targets", accessRevoke.ID, accessArgs(`,"user_id":7,"team_id":3`)},
		{"revoke no target", accessRevoke.ID, accessArgs("")},
		{"revoke with right", accessRevoke.ID, accessArgs(`,"user_id":7,"right":"read"`)},
		{"revoke extra field", accessRevoke.ID, accessArgs(`,"user_id":7,"method":"PUT"`)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			_, err := env.invokeConfirmed(tt.tool, "access", tt.args)
			if err == nil {
				t.Fatal("a malformed request was accepted")
			}
			if strings.Contains(err.Error(), inviteeLocal) || strings.Contains(err.Error(), testDomain) {
				t.Fatalf("err = %v shows an address", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

// A user or team that the drive does not list is refused after the membership read and before any change.
func TestAccessRefusesForeignUsersAndTeams(t *testing.T) {
	cases := []struct {
		name, tool, args string
		canary           int64
	}{
		{"grant user", accessGrant.ID, accessArgs(fmt.Sprintf(`,"right":"read","lang":"en","user_ids":[%d,%d]`,
			memberUser, foreignUser+1)), foreignUser + 1},
		{"grant team", accessGrant.ID, accessArgs(fmt.Sprintf(`,"right":"read","lang":"en","team_ids":[%d]`,
			foreignTeam+1)), foreignTeam + 1},
		{"grant locked user", accessGrant.ID, accessArgs(fmt.Sprintf(`,"right":"read","lang":"en","user_ids":[%d]`,
			foreignUser)), foreignUser},
		{"grant team of a locked user", accessGrant.ID, accessArgs(fmt.Sprintf(`,"right":"read","lang":"en","team_ids":[%d]`,
			foreignTeam)), foreignTeam},
		{"update user", accessUpdate.ID, accessArgs(fmt.Sprintf(`,"user_id":%d,"right":"read"`, foreignUser+1)), foreignUser + 1},
		{"update team", accessUpdate.ID, accessArgs(fmt.Sprintf(`,"team_id":%d,"right":"read"`, foreignTeam+1)), foreignTeam + 1},
		{"revoke user", accessRevoke.ID, accessArgs(fmt.Sprintf(`,"user_id":%d`, foreignUser+1)), foreignUser + 1},
		{"revoke team", accessRevoke.ID, accessArgs(fmt.Sprintf(`,"team_id":%d`, foreignTeam+1)), foreignTeam + 1},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, membership(func(r *http.Request) (*http.Response, error) {
				t.Fatalf("request %s %s reached the change", r.Method, r.URL.Path)
				return nil, nil
			}), nil)
			_, err := env.invokeConfirmed(tt.tool, "access", tt.args)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), fmt.Sprint(tt.canary)) {
				t.Fatalf("err = %v, want an invalid request without the foreign value", err)
			}
			if len(calls) != 2 || calls[1].path != usersPathOf() {
				t.Fatalf("calls = %+v, want the ownership check and the membership read only", calls)
			}
		})
	}
}

func TestAccessMembershipReadsFurtherPagesAndRefusesWhatItCannotProve(t *testing.T) {
	args := accessArgs(fmt.Sprintf(`,"user_id":%d,"right":"read"`, memberUser2))
	t.Run("found on a later page", func(t *testing.T) {
		var calls []call
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
			switch {
			case r.URL.Path == usersPathOf() && r.URL.Query().Get("page") == "1":
				return jsonResponse(200, usersPage(1, 2, memberJSONOf(memberUser, "active"))), nil
			case r.URL.Path == usersPathOf():
				return jsonResponse(200, usersPage(2, 2, memberJSONOf(memberUser2, "active"))), nil
			}
			return jsonResponse(200, envelopeSuccess(`true`)), nil
		}), nil)
		if _, err := env.invokeConfirmed(accessUpdate.ID, "access", args); err != nil || len(calls) != 4 ||
			calls[3].method != http.MethodPut {
			t.Fatalf("err = %v, calls = %+v", err, calls)
		}
	})
	t.Run("page count missing", func(t *testing.T) {
		var calls []call
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(200, envelopeSuccess(`[`+memberJSONOf(memberUser, "active")+`]`)), nil
		}), nil)
		_, err := env.invokeConfirmed(accessUpdate.ID, "access", args)
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), "completely") || len(calls) != 2 {
			t.Fatalf("err = %v, calls = %d, want a refusal as unproven after one read", err, len(calls))
		}
	})
	t.Run("too many pages", func(t *testing.T) {
		var calls []call
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == usersPathOf() {
				return jsonResponse(200, usersPage(1, 500, memberJSONOf(memberUser, "active"))), nil
			}
			t.Fatalf("request %s %s reached the change", r.Method, r.URL.Path)
			return nil, nil
		}), nil)
		_, err := env.invokeConfirmed(accessUpdate.ID, "access", args)
		if !isInvalidRequest(err) || !strings.Contains(err.Error(), "completely") || len(calls) != 1+maxMemberPages {
			t.Fatalf("err = %v, calls = %d, want a refusal as unproven after %d reads", err, len(calls), maxMemberPages)
		}
	})
	t.Run("read fails", func(t *testing.T) {
		var calls []call
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(403, `{"result":"error"}`), nil
		}), nil)
		_, err := env.invokeConfirmed(accessUpdate.ID, "access", args)
		if classOf(err) != provider.ClassPermission || len(calls) != 2 {
			t.Fatalf("err = %v, calls = %d, want permission and no change", err, len(calls))
		}
	})
}

func TestAccessChangeErrorsAndUncertainOutcomesAreNeverRepeated(t *testing.T) {
	statuses := []struct {
		status int
		class  provider.Class
		want   string
	}{
		{403, provider.ClassPermission, "plan"},
		{404, provider.ClassNotFound, "does not hold"},
		{500, provider.ClassProviderError, "may have been applied"},
		{503, provider.ClassUnreachable, "may have been applied"},
	}
	failures := map[string]func(*http.Request) (*http.Response, error){
		"timeout": func(*http.Request) (*http.Response, error) { return nil, timeoutError{} },
		"garbage": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
		"error result": func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"result":"error"}`), nil
		},
		"false flag": func(*http.Request) (*http.Response, error) { return jsonResponse(200, envelopeSuccess(`false`)), nil },
		"empty feedback": func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, envelopeSuccess(`{}`)), nil
		},
		"flag for a grant": func(*http.Request) (*http.Response, error) { return jsonResponse(200, envelopeSuccess(`true`)), nil },
	}
	for _, tt := range accessCases() {
		for _, s := range statuses {
			t.Run(fmt.Sprintf("%s %d", tt.name, s.status), func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, membership(func(r *http.Request) (*http.Response, error) {
					return jsonResponse(s.status, `{"result":"error","error":{"description":"`+foreignCanary+" "+inviteeEmail+`"}}`), nil
				}), nil)
				_, err := env.invokeConfirmed(tt.tool, "access", tt.args)
				if classOf(err) != s.class || err == nil || !strings.Contains(err.Error(), s.want) ||
					strings.Contains(err.Error(), foreignCanary) || strings.Contains(err.Error(), inviteeLocal) ||
					len(calls) != 3 {
					t.Fatalf("err = %v, calls = %d, want class %s mentioning %q after one change", err, len(calls), s.class, s.want)
				}
			})
		}
		for name, failure := range failures {
			if tt.name != "grant" && (name == "empty feedback" || name == "flag for a grant") ||
				tt.name == "grant" && name == "false flag" {
				continue
			}
			t.Run(tt.name+" "+name, func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, membership(failure), nil)
				_, err := env.invokeConfirmed(tt.tool, "access", tt.args)
				if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(calls) != 3 ||
					strings.Contains(err.Error(), inviteeLocal) {
					t.Fatalf("err = %v, calls = %d, want the uncertainty after one change", err, len(calls))
				}
			})
		}
		t.Run(tt.name+" asynchronous", func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, membership(func(r *http.Request) (*http.Response, error) {
				return jsonResponse(200, `{"result":"asynchronous","data":null}`), nil
			}), nil)
			result, err := env.invokeConfirmed(tt.tool, "access", tt.args)
			var out AccessChange
			if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.Status != statusPending || len(calls) != 3 {
				t.Fatalf("result = %s, %v, calls = %d, want pending", result, err, len(calls))
			}
		})
	}
}

func TestAccessGetBoundsEntriesAndStrings(t *testing.T) {
	long := strings.Repeat("é", 400)
	var many []string
	for i := 1; i <= maxAccessEntries+5; i++ {
		many = append(many, fmt.Sprintf(`{"id":%d,"access":"user","name":%q,"right":"read","color":1,"status":"accepted"}`, i, long))
	}
	body := fmt.Sprintf(`{"users":[%s],"teams":[{"id":3,"access":"team","name":"Team\u0007 A","right":null,"status":"pending"}],`+
		`"invitations":[{"id":9,"access":"invitation","name":%q,"right":"write","status":"pending"}]}`,
		strings.Join(many, ","), inviteeEmail)
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, envelopeSuccess(body)), nil
	}), nil)
	result, err := env.invoke(accessGet.ID, "access", accessArgs(""))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if len(calls) != 2 || calls[1].method != http.MethodGet || calls[1].path != accessPathOf(childFileID) ||
		calls[1].query.Encode() != (url.Values{}).Encode() || calls[1].body != "" {
		t.Fatalf("calls = %+v, want the ownership check and one read", calls)
	}
	var out AccessList
	if err := json.Unmarshal([]byte(result), &out); err != nil || len(out.Users) != maxAccessEntries || !out.Truncated ||
		len(out.Users[0].Name) > maxAccessText || !strings.HasPrefix(out.Users[0].Name, "é") ||
		len(out.Teams) != 1 || out.Teams[0].Name != "Team A" || out.Teams[0].Right != "" ||
		len(out.Invitations) != 1 || out.Invitations[0].Name != inviteeEmail || out.Invitations[0].Right != "write" {
		t.Fatalf("result = %.400s, %v", result, err)
	}
	if !json.Valid([]byte(result)) || strings.ContainsRune(out.Users[0].Name, '�') {
		t.Fatalf("a bounded name was cut inside a character")
	}
}

func TestAccessGetErrors(t *testing.T) {
	cases := map[int]provider.Class{403: provider.ClassPermission, 404: provider.ClassNotFound}
	for status, class := range cases {
		var calls []call
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
			return jsonResponse(status, `{"result":"error","error":{"description":"`+foreignCanary+`"}}`), nil
		}), nil)
		_, err := env.invoke(accessGet.ID, "access", accessArgs(""))
		if classOf(err) != class || strings.Contains(err.Error(), foreignCanary) {
			t.Fatalf("status %d: err = %v, want class %s", status, err, class)
		}
	}
}

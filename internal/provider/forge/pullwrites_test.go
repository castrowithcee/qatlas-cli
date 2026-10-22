package forge

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	headSHA  = "0123456789abcdef0123456789abcdef01234567"
	movedSHA = "fedcba9876543210fedcba9876543210fedcba98"
)

// writablePull is pull request 7 of acme/widget with a full head commit; state and merged vary.
func writablePull(p *product, state string, merged bool, head string) string {
	body := strings.Replace(widgetPull(p, 7), `"sha":"c2"`, `"sha":"`+head+`"`, 1)
	body = strings.Replace(body, `"state":"open"`, `"state":"`+state+`"`, 1)
	if merged {
		body = strings.Replace(body, `"merged":false`, `"merged":true,"merge_commit_sha":"deadbeef01"`, 1)
	}
	return body
}

// writeServer answers pull request 7 with the given state and the changes with change.
func writeServer(p *product, state string, merged bool, head string, change func(call) *http.Response) func(call) *http.Response {
	return func(c call) *http.Response {
		if c.method == http.MethodGet && c.path == "/api/v1/repos/acme/widget/pulls/7" {
			return jsonResponse(200, writablePull(p, state, merged, head))
		}
		if change != nil {
			return change(c)
		}
		return notFoundResponse()
	}
}

func TestPullRequestCreateSendsProductMembers(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response {
			if c.method == http.MethodPost && c.path == "/api/v1/repos/acme/widget/pulls" {
				return jsonResponse(201, widgetPull(p, 7))
			}
			return notFoundResponse()
		})
		arguments := `{"title":"Fix","head":"fork:feature","base":"main","assignees":["bob"],"labels":[1],` +
			`"milestone":2,"allow_maintainer_edit":true`
		// Forgejo refuses the Gitea-only members, Gitea content_version, which belongs to an update.
		other := `,"reviewers":["carol"]}`
		if p == gitea {
			other = `,"content_version":1}`
		}
		if _, err := e.invoke(t, pullRequestsCreate, "repo", arguments+other); !isInvalidRequest(err) ||
			len(*e.calls) != 0 {
			t.Errorf("%s: foreign member: %v, calls %v", p.id, err, methods(e))
		}
		other = `}`
		if p == gitea {
			other = `,"reviewers":["carol"],"team_reviewers":["core"]}`
		}
		result, err := e.invoke(t, pullRequestsCreate, "repo", arguments+other)
		if err != nil || result["number"] != float64(7) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		last := (*e.calls)[len(*e.calls)-1]
		want := `"head":"fork:feature"`
		if last.method != http.MethodPost || !strings.Contains(last.body, want) || strings.Contains(last.body, "sudo") ||
			strings.Contains(last.body, "reviewers") != (p == gitea) {
			t.Errorf("%s: %s %s", p.id, last.method, last.body)
		}
		for _, bad := range []string{`{"title":"x","base":"main"}`, `{"head":"a","base":"main"}`,
			`{"title":"x","head":"a:b:c","base":"main"}`, `{"title":"x","head":"a","base":"x..y"}`} {
			before := len(*e.calls)
			if _, err := e.invoke(t, pullRequestsCreate, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
		// Without confirmation nothing is sent.
		before := len(*e.calls)
		if _, err := e.invokeAs(t, pullRequestsCreate, "repo", arguments+other, false); err == nil || len(*e.calls) != before {
			t.Errorf("%s: unconfirmed create: %v", p.id, err)
		}
	}
}

func TestPullRequestWritesRefuseTargetsBeforeTheToken(t *testing.T) {
	cases := map[*tool]string{
		pullRequestsCreate:          `{"repository":"other/place","title":"x","head":"a","base":"b"}`,
		pullRequestsUpdate:          `{"repository":"other/place","number":7,"title":"x"}`,
		pullRequestsClose:           `{"repository":"other/place","number":7}`,
		pullRequestsReopen:          `{"repository":"other/place","number":7}`,
		pullRequestsCancelAutoMerge: `{"repository":"other/place","number":7}`,
	}
	for _, p := range products {
		for tl, arguments := range cases {
			e := newEnvironment(t, p, writeServer(p, "open", false, headSHA, nil))
			_, err := e.invoke(t, tl, "repo", arguments)
			if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 || strings.Contains(err.Error(), "other") {
				t.Errorf("%s %s: err %v, calls %d, reads %d", p.id, tl.action, err, len(*e.calls), *e.reads)
			}
		}
		// A foreign base repository in the evidence read stops the change.
		e := newEnvironment(t, p, pullServer(p, func(c call) *http.Response {
			t.Errorf("%s: request %s %s after the evidence read", p.id, c.method, c.path)
			return notFoundResponse()
		}))
		for tl, arguments := range map[*tool]string{pullRequestsUpdate: `{"number":9,"title":"x"}`,
			pullRequestsClose: `{"number":9}`, pullRequestsCancelAutoMerge: `{"number":9}`} {
			if _, err := e.invoke(t, tl, "repo", arguments); !isInvalidRequest(err) {
				t.Errorf("%s %s: %v", p.id, tl.action, err)
			}
		}
	}
}

func TestPullRequestUpdateSendsProductMembers(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, writeServer(p, "open", false, headSHA, func(c call) *http.Response {
			if c.method == http.MethodPatch && c.path == "/api/v1/repos/acme/widget/pulls/7" {
				return jsonResponse(201, writablePull(p, "open", false, headSHA))
			}
			return notFoundResponse()
		}))
		if _, err := e.invoke(t, pullRequestsUpdate, "repo", `{"number":7,"title":"New","unset_due_date":true,"milestone":0}`); err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		got := (*e.calls)[1]
		if got.method != http.MethodPatch || got.body != `{"milestone":0,"title":"New","unset_due_date":true}` {
			t.Errorf("%s: %s %s", p.id, got.method, got.body)
		}
		before := len(*e.calls)
		bad := []string{`{"number":7}`, `{"number":7,"state":"closed"}`, `{"number":7,"reviewers":["x"]}`,
			`{"number":7,"due_date":"2030-01-01T00:00:00Z","unset_due_date":true}`, `{"number":7,"head":"x"}`}
		if p == forgejo {
			bad = append(bad, `{"number":7,"content_version":1}`)
		}
		for _, arguments := range bad {
			if _, err := e.invoke(t, pullRequestsUpdate, "repo", arguments); err == nil || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, arguments, err)
			}
		}
		if p == gitea {
			if _, err := e.invoke(t, pullRequestsUpdate, "repo", `{"number":7,"content_version":4,"allow_maintainer_edit":false}`); err != nil {
				t.Fatalf("gitea: %v", err)
			}
			if body := (*e.calls)[len(*e.calls)-1].body; body != `{"allow_maintainer_edit":false,"content_version":4}` {
				t.Errorf("gitea body %s", body)
			}
		}
	}
}

func TestPullRequestCloseAndReopenAreIdempotent(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{pullRequestsClose, pullRequestsReopen} {
			from, to := "open", "closed"
			if tl == pullRequestsReopen {
				from, to = "closed", "open"
			}
			e := newEnvironment(t, p, writeServer(p, from, false, headSHA, func(c call) *http.Response {
				if c.method == http.MethodPatch {
					return jsonResponse(201, writablePull(p, to, false, headSHA))
				}
				return notFoundResponse()
			}))
			result, err := e.invoke(t, tl, "repo", `{"number":7}`)
			if err != nil || result["changed"] != true || result["state"] != to ||
				(*e.calls)[1].body != `{"state":"`+to+`"}` {
				t.Errorf("%s %s: %v, %v, %v", p.id, tl.action, result, err, *e.calls)
			}
			same := newEnvironment(t, p, writeServer(p, to, false, headSHA, func(c call) *http.Response {
				t.Errorf("%s %s: change request %s", p.id, tl.action, c.method)
				return notFoundResponse()
			}))
			result, err = same.invoke(t, tl, "repo", `{"number":7}`)
			if err != nil || result["changed"] != false || len(*same.calls) != 1 {
				t.Errorf("%s %s: %v, %v", p.id, tl.action, result, err)
			}
		}
		merged := newEnvironment(t, p, writeServer(p, "closed", true, headSHA, nil))
		if _, err := merged.invoke(t, pullRequestsReopen, "repo", `{"number":7}`); !isInvalidRequest(err) ||
			len(*merged.calls) != 1 {
			t.Errorf("%s: reopen merged: %v", p.id, err)
		}
	}
}

func TestPullRequestCancelAutoMergeDeletesTheScheduledMerge(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, writeServer(p, "open", false, headSHA, func(c call) *http.Response {
			if c.method == http.MethodDelete && c.path == "/api/v1/repos/acme/widget/pulls/7/merge" {
				return jsonResponse(204, "")
			}
			return notFoundResponse()
		}))
		result, err := e.invoke(t, pullRequestsCancelAutoMerge, "repo", `{"number":7}`)
		if err != nil || result["cancelled"] != true || (*e.calls)[1].body != "" {
			t.Errorf("%s: %v, %v", p.id, result, err)
		}
		before := len(*e.calls)
		if _, err := e.invokeAs(t, pullRequestsCancelAutoMerge, "repo", `{"number":7}`, false); err == nil || len(*e.calls) != before {
			t.Errorf("%s: unconfirmed: %v", p.id, err)
		}
	}
}

func mergeArgs(extra string) string {
	return `{"number":7,"sha":"` + headSHA + `","do":"squash"` + extra + `}`
}

func TestPullRequestMergeBodiesFollowTheProduct(t *testing.T) {
	for _, p := range products {
		reads := 0
		e := newEnvironment(t, p, func(c call) *http.Response {
			switch {
			case c.method == http.MethodGet:
				reads++
				return jsonResponse(200, writablePull(p, "open", reads > 1, headSHA))
			case c.method == http.MethodPost && c.path == "/api/v1/repos/acme/widget/pulls/7/merge":
				return jsonResponse(200, "")
			}
			return notFoundResponse()
		})
		result, err := e.invoke(t, pullRequestsMerge, "delete", mergeArgs(
			`,"merge_title":"T","merge_message":"M","delete_branch_after_merge":true,"merge_when_checks_succeed":false`))
		if err != nil || result["merged"] != true || result["changed"] != true || result["merge_commit_sha"] != "deadbeef01" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		want := `{"MergeMessageField":"M","MergeTitleField":"T","delete_branch_after_merge":true,"Do":"squash",` +
			`"head_commit_id":"` + headSHA + `","merge_when_checks_succeed":false}`
		if p == gitea {
			want = `{"delete_branch_after_merge":true,"do":"squash","head_commit_id":"` + headSHA +
				`","merge_message_field":"M","merge_title_field":"T","merge_when_checks_succeed":false}`
		}
		post := (*e.calls)[1]
		if post.method != http.MethodPost || normalizeJSON(post.body) != normalizeJSON(want) ||
			strings.Contains(strings.ToLower(post.body), "force_merge") {
			t.Errorf("%s: body %s, want %s", p.id, post.body, want)
		}
		if got := methods(e); len(got) != 3 || got[0][:3] != "GET" {
			t.Errorf("%s calls = %v", p.id, got)
		}
	}
}

func normalizeJSON(text string) string {
	// Compare member sets, independent of the order.
	parts := strings.Split(strings.Trim(text, "{}"), ",")
	for i := range parts {
		for j := i + 1; j < len(parts); j++ {
			if parts[j] < parts[i] {
				parts[i], parts[j] = parts[j], parts[i]
			}
		}
	}
	return strings.Join(parts, ",")
}

func TestPullRequestMergeManuallyMergedNeedsTheCommitAndOnlyThen(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, writeServer(p, "open", false, headSHA, func(c call) *http.Response {
			return jsonResponse(200, "")
		}))
		for _, bad := range []string{
			`{"number":7,"sha":"` + headSHA + `","do":"manually-merged"}`,
			mergeArgs(`,"merge_commit_id":"abcdef1"`),
			`{"number":7,"sha":"abc","do":"merge"}`,
			`{"number":7,"sha":"` + headSHA + `","do":"force"}`,
			mergeArgs(`,"force_merge":true`),
		} {
			if _, err := e.invoke(t, pullRequestsMerge, "delete", bad); !isInvalidRequest(err) || len(*e.calls) != 0 {
				t.Errorf("%s %s: %v, calls %v", p.id, bad, err, methods(e))
			}
		}
		if _, err := e.invoke(t, pullRequestsMerge, "delete",
			`{"number":7,"sha":"`+headSHA+`","do":"manually-merged","merge_commit_id":"abcdef1"}`); err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		if body := (*e.calls)[1].body; !strings.Contains(body, "abcdef1") {
			t.Errorf("%s: body %s", p.id, body)
		}
	}
}

func TestPullRequestMergeChecksTheHeadBeforeAnyMergeRequest(t *testing.T) {
	for _, p := range products {
		// Already merged with exactly this head: success without a merge request.
		e := newEnvironment(t, p, writeServer(p, "closed", true, headSHA, func(c call) *http.Response {
			t.Errorf("%s: request %s %s", p.id, c.method, c.path)
			return notFoundResponse()
		}))
		result, err := e.invoke(t, pullRequestsMerge, "delete", mergeArgs(""))
		if err != nil || result["merged"] != true || result["changed"] != false || len(*e.calls) != 1 {
			t.Errorf("%s: already merged: %v, %v", p.id, result, err)
		}
		// Merged with another head, or an open pull request whose head moved: both commits, no merge request.
		for _, merged := range []bool{true, false} {
			state := "open"
			if merged {
				state = "closed"
			}
			e = newEnvironment(t, p, writeServer(p, state, merged, movedSHA, func(c call) *http.Response {
				t.Errorf("%s: request %s %s", p.id, c.method, c.path)
				return notFoundResponse()
			}))
			_, err = e.invoke(t, pullRequestsMerge, "delete", mergeArgs(""))
			if !isInvalidRequest(err) || !strings.Contains(err.Error(), headSHA) || !strings.Contains(err.Error(), movedSHA) ||
				len(*e.calls) != 1 {
				t.Errorf("%s merged=%v: %v", p.id, merged, err)
			}
		}
	}
}

func TestPullRequestMergeMapsRefusalsWithoutProviderText(t *testing.T) {
	cases := map[int]struct {
		invalid bool
		part    string
	}{
		409: {true, movedSHA}, 405: {false, "not mergeable"}, 423: {false, "archived or locked"},
		413: {false, "quota"}, 500: {false, "may have been applied"},
	}
	for _, p := range products {
		for status, want := range cases {
			reads := 0
			e := newEnvironment(t, p, func(c call) *http.Response {
				if c.method == http.MethodGet {
					reads++
					head := headSHA
					if reads > 1 {
						head = movedSHA
					}
					return jsonResponse(200, writablePull(p, "open", false, head))
				}
				return jsonResponse(status, `{"message":"`+bodyCanary+`"}`)
			})
			_, err := e.invoke(t, pullRequestsMerge, "delete", mergeArgs(""))
			posts := 0
			for _, c := range *e.calls {
				if c.method == http.MethodPost {
					posts++
				}
			}
			if err == nil || posts != 1 || !strings.Contains(err.Error(), want.part) ||
				isInvalidRequest(err) != want.invalid || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s %d: %v, posts %d", p.id, status, err, posts)
			}
			if status == 409 && (!strings.Contains(err.Error(), headSHA) || len(*e.calls) != 3) {
				t.Errorf("%s: 409 names %v, calls %v", p.id, err, methods(e))
			}
		}
		// A timeout is an open outcome and is not repeated.
		e := newEnvironment(t, p, writeServer(p, "open", false, headSHA, nil))
		inner := transport
		transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost {
				return inner.RoundTrip(r)
			}
			*e.calls = append(*e.calls, call{method: r.Method, path: r.URL.Path})
			return nil, context.DeadlineExceeded
		})
		_, err := e.invoke(t, pullRequestsMerge, "delete", mergeArgs(""))
		var failure *provider.Error
		if !errors.As(err, &failure) || !strings.HasSuffix(failure.Message, uncertain) || len(*e.calls) != 2 {
			t.Errorf("%s: timeout: %v, calls %v", p.id, err, methods(e))
		}
	}
}

func TestPullRequestBranchUpdateChecksTheExpectedHeadFirst(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, writeServer(p, "open", false, headSHA, func(c call) *http.Response {
			if c.method == http.MethodPost && c.path == "/api/v1/repos/acme/widget/pulls/7/update" {
				return jsonResponse(200, "")
			}
			return notFoundResponse()
		}))
		stale := `{"number":7,"expected_head_sha":"` + movedSHA + `"}`
		if _, err := e.invoke(t, pullRequestBranchesUpdate, "delete", stale); !isInvalidRequest(err) ||
			len(*e.calls) != 1 || (*e.calls)[0].method != http.MethodGet {
			t.Errorf("%s: stale head: %v, calls %v", p.id, err, methods(e))
		}
		result, err := e.invoke(t, pullRequestBranchesUpdate, "delete",
			`{"number":7,"expected_head_sha":"`+headSHA+`","style":"rebase"}`)
		if err != nil || result["updated"] != true {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		last := (*e.calls)[len(*e.calls)-1]
		if last.method != http.MethodPost || last.query != "style=rebase" || last.body != "" {
			t.Errorf("%s: %s ?%s %q", p.id, last.method, last.query, last.body)
		}
		before := len(*e.calls)
		for _, bad := range []string{`{"number":7}`, `{"number":7,"expected_head_sha":"abc"}`,
			`{"number":7,"expected_head_sha":"` + headSHA + `","style":"squash"}`} {
			if _, err := e.invoke(t, pullRequestBranchesUpdate, "delete", bad); !isInvalidRequest(err) || len(*e.calls) != before {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
	}
}

func TestPullRequestMergeAndBranchUpdateAreListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		for _, tl := range []*tool{pullRequestsMerge, pullRequestBranchesUpdate} {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
				t.Fatalf("%s descriptor = %+v", p.toolID(tl), d)
			}
			metadata, _ := reg.ProviderMetadata(p.id)
			for _, profile := range metadata.Profiles {
				for _, id := range profile.Tools {
					if id == d.ID {
						t.Errorf("profile %s holds %s", profile.ID, id)
					}
				}
			}
			e := newEnvironment(t, p, writeServer(p, "open", false, headSHA, nil))
			arguments := mergeArgs("")
			if tl == pullRequestBranchesUpdate {
				arguments = `{"number":7,"expected_head_sha":"` + headSHA + `"}`
			}
			if _, err := e.invoke(t, tl, "repo", arguments); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s offered without a tools list: %v", d.ID, err)
			}
		}
		for _, tl := range []*tool{pullRequestsCreate, pullRequestsUpdate, pullRequestsClose, pullRequestsReopen,
			pullRequestsCancelAutoMerge} {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok || d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
				t.Errorf("%s descriptor = %+v", p.toolID(tl), d)
			}
		}
	}
}

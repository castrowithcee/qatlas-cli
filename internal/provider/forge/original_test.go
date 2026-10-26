package forge

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const originalDocs = "https://forge.example/api/swagger"

func originalOf(t *testing.T, err error) *provider.Original {
	t.Helper()
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Original == nil {
		t.Fatalf("no original details: %v", err)
	}
	return failure.Original
}

func wantOriginal(t *testing.T, label string, err error, status int) {
	t.Helper()
	o := originalOf(t, err)
	if o.Status != status || len(o.Entries) != 1 || o.Entries[0].Message != bodyCanary ||
		o.Documentation != originalDocs {
		t.Errorf("%s: original %+v", label, o)
	}
}

func originalBody() string { return `{"message":"` + bodyCanary + `","url":"` + originalDocs + `"}` }

func TestRefusalCarriesTheOriginalAnswer(t *testing.T) {
	want := map[int]provider.Class{401: provider.ClassAuth, 403: provider.ClassNotFound,
		404: provider.ClassNotFound, 422: provider.ClassProviderError}
	for _, p := range products {
		for status, class := range want {
			e := newEnvironment(t, p, func(call) *http.Response { return jsonResponse(status, originalBody()) })
			_, err := e.invoke(t, usersGet, "open", `{"username":"octo"}`)
			if err == nil {
				t.Fatalf("%s %d: no error", p.id, status)
			}
			if classOfError(err) != class || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s %d: class %s, error %v", p.id, status, classOfError(err), err)
			}
			wantOriginal(t, p.id, err, status)
		}
	}
}

func TestBranchUpdateRefusalKeepsTheOriginalAnswer(t *testing.T) {
	for _, p := range products {
		for _, status := range []int{409, 422, 413} {
			e := newEnvironment(t, p, writeServer(p, "open", false, headSHA, func(c call) *http.Response {
				if c.method == http.MethodPost {
					return jsonResponse(status, originalBody())
				}
				return notFoundResponse()
			}))
			_, err := e.invoke(t, pullRequestBranchesUpdate, "delete",
				`{"number":7,"expected_head_sha":"`+headSHA+`"}`)
			if err == nil || classOfError(err) != provider.ClassProviderError || strings.Contains(err.Error(), bodyCanary) {
				t.Fatalf("%s %d: %v", p.id, status, err)
			}
			wantOriginal(t, p.id, err, status)
		}
	}
}

func TestMergeRefusalKeepsTheOriginalAnswer(t *testing.T) {
	for _, p := range products {
		for _, status := range []int{405, 423, 413} {
			e := newEnvironment(t, p, func(c call) *http.Response {
				if c.method == http.MethodGet {
					return jsonResponse(200, writablePull(p, "open", false, headSHA))
				}
				return jsonResponse(status, originalBody())
			})
			_, err := e.invoke(t, pullRequestsMerge, "delete", mergeArgs(""))
			if err == nil || strings.Contains(err.Error(), bodyCanary) {
				t.Fatalf("%s %d: %v", p.id, status, err)
			}
			wantOriginal(t, p.id, err, status)
		}
	}
}

func TestReviewRefusalKeepsTheOriginalAnswer(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, reviewServer(p, answerWith(422, originalBody())))
		_, err := e.invoke(t, reviewsApprove, "reviews", `{"number":7}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) {
			t.Fatalf("%s: %v", p.id, err)
		}
		wantOriginal(t, p.id, err, 422)
	}
}

func TestDisabledTimesKeepTheOriginalAnswer(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, issueServer(p, "open", func(c call) *http.Response {
			if strings.Contains(c.path, "/times") {
				return jsonResponse(400, originalBody())
			}
			return notFoundResponse()
		}))
		_, err := e.invoke(t, issueTimesList, "repo", `{"number":7}`)
		if err == nil || !strings.Contains(err.Error(), "not available") || strings.Contains(err.Error(), bodyCanary) {
			t.Fatalf("%s: %v", p.id, err)
		}
		wantOriginal(t, p.id, err, 400)
	}
}

func TestSecretSetRefusalKeepsOnlyTheStatus(t *testing.T) {
	for _, p := range products {
		e := newAdminEnvironment(t, p, defaultVersion(p), func(c call) *http.Response {
			return jsonResponse(422, `{"message":"`+strings.ReplaceAll(c.body, `"`, `'`)+`","url":"`+originalDocs+`"}`)
		})
		_, err := e.call(t, actionsSecretsSet, "admin-repo", `{"name":"DEPLOY_KEY","secret_ref":"`+releasedRef+`"}`, true)
		if err == nil {
			t.Fatalf("%s: no error", p.id)
		}
		if o := originalOf(t, err); o.Status != 422 || len(o.Entries) != 0 || o.Documentation != "" {
			t.Errorf("%s: original %+v", p.id, o)
		}
	}
}

func notFoundWithOriginal(c call, rest func(call) *http.Response) *http.Response {
	if strings.Contains(c.path, "/quota") {
		return jsonResponse(404, originalBody())
	}
	return rest(c)
}

func TestSwitchedOffQuotaKeepsTheOriginalAnswer(t *testing.T) {
	e := newEnvironment(t, forgejo, func(c call) *http.Response {
		return notFoundWithOriginal(c, func(c call) *http.Response {
			if c.path == "/api/v1/user" {
				return jsonResponse(200, `{"id":1,"login":"alice"}`)
			}
			return notFoundResponse()
		})
	})
	_, err := e.invoke(t, quotaGet, "open", `{}`)
	if err == nil || classOfError(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "not available") ||
		strings.Contains(err.Error(), bodyCanary) {
		t.Fatalf("quota: %v", err)
	}
	wantOriginal(t, "quota", err, 404)
}

func TestSwitchedOffAdminQuotaKeepsTheOriginalAnswer(t *testing.T) {
	e := newEnvironment(t, forgejo, func(c call) *http.Response {
		return notFoundWithOriginal(c, func(c call) *http.Response {
			if c.path == "/api/v1/admin/users" {
				return jsonResponse(200, `[]`)
			}
			return notFoundResponse()
		})
	})
	_, err := e.invoke(t, quotaAdminGroupsGet, "open", `{"group":"std"}`)
	if err == nil || classOfError(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "not available") ||
		strings.Contains(err.Error(), bodyCanary) {
		t.Fatalf("admin quota: %v", err)
	}
	wantOriginal(t, "admin quota", err, 404)
}

func TestSubscriptionNotModifiedKeepsTheOriginalAnswer(t *testing.T) {
	for _, p := range products {
		status := 304
		e := newEnvironment(t, p, subscriptionServer(p, &status))
		_, err := e.invoke(t, issueSubscriptionsAdd, "repo", `{"number":7,"user":"bob"}`)
		if err == nil || !strings.Contains(err.Error(), "administrator") {
			t.Fatalf("%s: %v", p.id, err)
		}
		if o := originalOf(t, err); o.Status != 304 {
			t.Errorf("%s: original %+v", p.id, o)
		}
	}
}

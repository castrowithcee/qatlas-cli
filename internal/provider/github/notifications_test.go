package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const notificationPrefix = "/api/v3"

// fakeNotifications answers the notification routes through the failure hook of fakeGitHub. Thread 100 belongs
// to octo-org/example, 200 to hubot/elsewhere, and 300 to octo-org/other; every other thread is unknown.
type fakeNotifications struct {
	title string
	f     *fakeGitHub
}

// body is the JSON body of the request being answered, which fakeGitHub has already read and recorded.
func (n *fakeNotifications) body() map[string]any {
	requests := n.f.recorded()
	return requests[len(requests)-1].body
}

func notificationThreadJSON(id, repository, title string) string {
	owner, _, _ := strings.Cut(repository, "/")
	return fmt.Sprintf(`{"id":%q,"unread":true,"reason":"mention","updated_at":"2026-01-02T00:00:00Z",`+
		`"last_read_at":null,"subject":{"title":%q,"url":"https://api.github.com/repos/%s/issues/1",`+
		`"latest_comment_url":null,"type":"Issue"},"repository":{"full_name":%q,"owner":{"login":%q}}}`,
		id, title, repository, repository, owner)
}

func (n *fakeNotifications) route(w http.ResponseWriter, r *http.Request) bool {
	path, ok := strings.CutPrefix(r.URL.Path, notificationPrefix)
	if !ok {
		return false
	}
	threads := map[string]string{
		"100": notificationThreadJSON("100", "octo-org/example", n.title),
		"200": notificationThreadJSON("200", "hubot/elsewhere", "foreign"),
		"300": notificationThreadJSON("300", "octo-org/other", "other"),
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && path == "/notifications":
		w.Header().Set("Link", `<https://api.github.com/notifications?page=2>; rel="next"`)
		fmt.Fprintf(w, "[%s,%s,%s]", threads["100"], threads["200"], threads["300"])
	case r.Method == http.MethodGet && path == "/repos/octo-org/example/notifications":
		fmt.Fprintf(w, "[%s]", threads["100"])
	case r.Method == http.MethodPut && path == "/notifications":
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"message":"Unread notifications couldn't be marked in a single request."}`)
	case r.Method == http.MethodPut && strings.HasSuffix(path, "/notifications"):
		w.WriteHeader(http.StatusResetContent)
	case strings.HasPrefix(path, "/notifications/threads/"):
		rest := strings.TrimPrefix(path, "/notifications/threads/")
		id, sub, _ := strings.Cut(rest, "/")
		thread, known := threads[id]
		switch {
		case !known:
			w.WriteHeader(http.StatusNotFound)
		case sub == "" && r.Method == http.MethodGet:
			fmt.Fprint(w, thread)
		case sub == "" && r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusResetContent)
		case sub == "" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case sub == "subscription" && r.Method == http.MethodPut:
			ignored, _ := n.body()["ignored"].(bool)
			fmt.Fprintf(w, `{"subscribed":%t,"ignored":%t,"reason":"manual"}`, !ignored, ignored)
		case sub == "subscription" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	case path == "/repos/octo-org/example/subscription" && r.Method == http.MethodPut:
		subscribed, _ := n.body()["subscribed"].(bool)
		ignored, _ := n.body()["ignored"].(bool)
		fmt.Fprintf(w, `{"subscribed":%t,"ignored":%t}`, subscribed, ignored)
	case path == "/repos/octo-org/example/subscription" && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		return false
	}
	return true
}

func notificationsConfig(base string) *config.Config {
	cfg := coreConfig(base)
	all := config.Permissions()
	add := func(name string, targets []string, tools ...string) {
		cfg.Connections[name] = config.Connection{Service: "gh", Credential: "gh-reader", Targets: targets,
			Permissions: all, Tools: tools}
	}
	add("open", nil)
	add("open-listed", nil, notificationsMarkAll.ID)
	add("org", []string{"orgs/octo-org"})
	add("org-listed", []string{"orgs/octo-org"}, notificationsMarkAll.ID)
	add("one", []string{repoTarget})
	add("one-listed", []string{repoTarget}, notificationsMarkAll.ID)
	add("two", []string{repoTarget, "repos/octo-org/other"})
	add("project", []string{projectTarget})
	return cfg
}

func notificationRig(t *testing.T) (*fakeGitHub, *application.Core, *int) {
	t.Helper()
	f := &fakeGitHub{}
	n := &fakeNotifications{title: "Crash " + strings.Repeat("x", notificationTitleLimit+20), f: f}
	f.failure = n.route
	red := &redact.Redactor{}
	reads := 0
	core := application.New(registry(t), notificationsConfig(serve(t, f)), resolver(red, &reads), red)
	return f, core, &reads
}

func sent(f *fakeGitHub, from int) []recorded {
	return f.recorded()[from:]
}

func TestNotificationsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f, core, _ := notificationRig(t)

	list, err := invoke(t, core, notificationsList.ID, "open",
		`{"participating":true,"since":"2026-01-01","before":"2026-02-01T00:00:00Z","limit":3}`, false)
	if err != nil || !strings.Contains(string(list), `"id":"100"`) || !strings.Contains(string(list), `"id":"200"`) ||
		!strings.Contains(string(list), `"title_truncated":true`) || !strings.Contains(string(list), `"has_more":true`) ||
		!strings.Contains(string(list), `"next_cursor":"`) {
		t.Fatalf("list = %s, %v", list, err)
	}
	query := f.recorded()[len(f.recorded())-1].query
	for _, want := range []string{"participating=true", "since=2026-01-01T00%3A00%3A00Z", "before=2026-02-01T00%3A00%3A00Z",
		"per_page=3", "page=1"} {
		if !strings.Contains(query, want) || strings.Contains(query, "all=") {
			t.Errorf("list query = %q, want %s and no all", query, want)
		}
	}
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	_ = json.Unmarshal(list, &page)
	if _, err := invoke(t, core, notificationsList.ID, "open", `{"participating":true,"since":"2026-01-01",`+
		`"before":"2026-02-01T00:00:00Z","cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Errorf("the next batch = %v", err)
	}
	// A cursor belongs to its filters and its repository.
	for _, arguments := range []string{`{"cursor":"` + page.NextCursor + `"}`,
		`{"repository":"octo-org/example","participating":true,"since":"2026-01-01","before":"2026-02-01T00:00:00Z",` +
			`"cursor":"` + page.NextCursor + `"}`} {
		if _, err := invoke(t, core, notificationsList.ID, "open", arguments, false); !isInvalidRequest(err) {
			t.Errorf("a foreign cursor = %v, want an invalid request", err)
		}
	}
	all, err := invoke(t, core, notificationsList.ID, "open", `{"include_read":true}`, false)
	if err != nil || !strings.Contains(f.recorded()[len(f.recorded())-1].query, "all=true") {
		t.Fatalf("include_read = %s, %v", all, err)
	}
	// The whole title never reaches the output: the cut is marked.
	if strings.Contains(string(all), strings.Repeat("x", notificationTitleLimit+1)) {
		t.Error("the title was not cut")
	}

	byRepository, err := invoke(t, core, notificationsList.ID, "open", `{"repository":"octo-org/example"}`, false)
	if err != nil || !strings.Contains(string(byRepository), `"repository":"octo-org/example"`) ||
		strings.Contains(string(byRepository), `"id":"200"`) ||
		f.recorded()[len(f.recorded())-1].path != notificationPrefix+"/repos/octo-org/example/notifications" {
		t.Errorf("repository list = %s, %v", byRepository, err)
	}

	got, err := invoke(t, core, notificationsGet.ID, "open", `{"thread_id":"100"}`, false)
	if err != nil || !strings.Contains(string(got), `"id":"100"`) || !strings.Contains(string(got), `"reason":"mention"`) ||
		!strings.Contains(string(got), `"subject_type":"Issue"`) {
		t.Fatalf("get = %s, %v", got, err)
	}

	for _, state := range []string{"read", "done"} {
		before := len(f.recorded())
		out, err := invoke(t, core, notificationsDismiss.ID, "open", `{"thread_id":"100","state":"`+state+`"}`, true)
		want := map[string]string{"read": http.MethodPatch, "done": http.MethodDelete}[state]
		requests := sent(f, before)
		if err != nil || !strings.Contains(string(out), `"state":"`+state+`"`) ||
			!strings.Contains(string(out), `"repository":"octo-org/example"`) || len(requests) != 2 ||
			requests[0].method != http.MethodGet || requests[1].method != want {
			t.Errorf("dismiss %s = %s, %v, requests %+v", state, out, err, requests)
		}
	}

	marked, err := invoke(t, core, notificationsMarkAll.ID, "open-listed", `{"last_read_at":"2026-01-05"}`, true)
	last := f.recorded()[len(f.recorded())-1]
	if err != nil || !strings.Contains(string(marked), `"marked":true`) || !strings.Contains(string(marked), `"queued":true`) ||
		last.path != notificationPrefix+"/notifications" || last.body["last_read_at"] != "2026-01-05T00:00:00Z" {
		t.Errorf("mark all = %s, %v, %+v", marked, err, last)
	}
	marked, err = invoke(t, core, notificationsMarkAll.ID, "open-listed", `{"repository":"octo-org/example"}`, true)
	if err != nil || !strings.Contains(string(marked), `"queued":false`) ||
		!strings.Contains(string(marked), `"repository":"octo-org/example"`) ||
		f.recorded()[len(f.recorded())-1].path != notificationPrefix+"/repos/octo-org/example/notifications" {
		t.Errorf("mark repository = %s, %v", marked, err)
	}

	for action, want := range map[string][2]string{"watch": {`"subscribed":true`, `"ignored":false`},
		"ignore": {`"subscribed":false`, `"ignored":true`}, "delete": {`"subscribed":false`, `"ignored":false`}} {
		before := len(f.recorded())
		out, err := invoke(t, core, threadSubscriptionsSet.ID, "open", `{"thread_id":"100","action":"`+action+`"}`, true)
		requests := sent(f, before)
		if err != nil || !strings.Contains(string(out), want[0]) || !strings.Contains(string(out), want[1]) ||
			len(requests) != 2 || requests[0].method != http.MethodGet {
			t.Errorf("thread subscription %s = %s, %v, requests %+v", action, out, err, requests)
		}
		out, err = invoke(t, core, repositorySubscriptionsSet.ID, "open", `{"repository":"octo-org/example","action":"`+action+`"}`, true)
		if err != nil || !strings.Contains(string(out), want[0]) || !strings.Contains(string(out), want[1]) ||
			!strings.Contains(string(out), `"repository":"octo-org/example"`) {
			t.Errorf("repository subscription %s = %s, %v", action, out, err)
		}
	}
}

func TestNotificationChangesNeedConfirmationAndMarkAllIsListedOnly(t *testing.T) {
	f, core, _ := notificationRig(t)
	unconfirmed := &application.ConfirmationRequiredError{}
	for _, call := range []struct{ tool, connection, arguments string }{
		{notificationsDismiss.ID, "open", `{"thread_id":"100","state":"read"}`},
		{notificationsMarkAll.ID, "open-listed", `{}`},
		{threadSubscriptionsSet.ID, "open", `{"thread_id":"100","action":"ignore"}`},
		{repositorySubscriptionsSet.ID, "open", `{"repository":"octo-org/example","action":"ignore"}`},
	} {
		before := len(f.recorded())
		if _, err := invoke(t, core, call.tool, call.connection, call.arguments, false); !errors.As(err, &unconfirmed) {
			t.Errorf("%s without --confirm = %v, want confirmation-required", call.tool, err)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s without --confirm reached GitHub", call.tool)
		}
	}
	var unsupported *capability.UnsupportedError
	if _, err := invoke(t, core, notificationsMarkAll.ID, "open", `{}`, true); !errors.As(err, &unsupported) {
		t.Errorf("mark all without a tools list = %v, want unsupported-capability", err)
	}
}

func TestNotificationTargetsAreCheckedBeforeAnySecretAccess(t *testing.T) {
	f, core, reads := notificationRig(t)
	refused := []struct{ name, tool, connection, arguments string }{
		{"list on a project connection", notificationsList.ID, "project", `{}`},
		{"list of several repositories without repository", notificationsList.ID, "two", `{}`},
		{"list of a repository outside the targets", notificationsList.ID, "one", `{"repository":"octo-org/other"}`},
		{"list of a repository of another owner", notificationsList.ID, "org", `{"repository":"hubot/elsewhere"}`},
		{"mark all of the account with an owner target", notificationsMarkAll.ID, "org-listed", `{}`},
		{"mark all of a foreign repository", notificationsMarkAll.ID, "org-listed", `{"repository":"hubot/elsewhere"}`},
		{"mark all with several repository targets", notificationsMarkAll.ID, "one-listed", `{"repository":"octo-org/other"}`},
		{"get on a project connection", notificationsGet.ID, "project", `{"thread_id":"100"}`},
		{"dismiss on a project connection", notificationsDismiss.ID, "project", `{"thread_id":"100","state":"read"}`},
		{"thread subscription on a project connection", threadSubscriptionsSet.ID, "project", `{"thread_id":"100","action":"watch"}`},
		{"repository subscription outside the targets", repositorySubscriptionsSet.ID, "one", `{"repository":"octo-org/other","action":"watch"}`},
		{"bad thread id", notificationsGet.ID, "open", `{"thread_id":"../user"}`},
		{"bad state", notificationsDismiss.ID, "open", `{"thread_id":"100","state":"deleted"}`},
		{"bad action", threadSubscriptionsSet.ID, "open", `{"thread_id":"100","action":"mute"}`},
		{"since after before", notificationsList.ID, "open", `{"since":"2026-02-01","before":"2026-01-01"}`},
	}
	for _, call := range refused {
		*reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, call.tool, call.connection, call.arguments, true); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want an invalid request", call.name, err)
		}
		if *reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s reached the credential or GitHub", call.name)
		}
	}

	// A connection with one repository target lists exactly that repository without an argument.
	out, err := invoke(t, core, notificationsList.ID, "one", `{}`, false)
	if err != nil || strings.Contains(string(out), `"id":"200"`) ||
		f.recorded()[len(f.recorded())-1].path != notificationPrefix+"/repos/octo-org/example/notifications" {
		t.Errorf("list on one repository = %s, %v", out, err)
	}
	// An owner target narrows the account-wide list to the repositories of that owner.
	out, err = invoke(t, core, notificationsList.ID, "org", `{}`, false)
	if err != nil || !strings.Contains(string(out), `"id":"100"`) || !strings.Contains(string(out), `"id":"300"`) ||
		strings.Contains(string(out), `"id":"200"`) {
		t.Errorf("list on an owner target = %s, %v", out, err)
	}
	if _, err := invoke(t, core, notificationsMarkAll.ID, "org-listed", `{"repository":"octo-org/other"}`, true); err != nil {
		t.Errorf("mark all of a repository of the owner target = %v", err)
	}
}

// A thread of a repository outside the targets is read but never changed, and answers like an unknown thread.
func TestNotificationThreadsOfOtherRepositoriesAreRefused(t *testing.T) {
	f, core, _ := notificationRig(t)
	for _, call := range []struct{ tool, connection, arguments string }{
		{notificationsGet.ID, "one", `{"thread_id":"200"}`},
		{notificationsGet.ID, "one", `{"thread_id":"300"}`},
		{notificationsDismiss.ID, "one", `{"thread_id":"300","state":"done"}`},
		{notificationsDismiss.ID, "org", `{"thread_id":"200","state":"read"}`},
		{threadSubscriptionsSet.ID, "one", `{"thread_id":"200","action":"ignore"}`},
		{threadSubscriptionsSet.ID, "open", `{"thread_id":"999","action":"ignore"}`},
	} {
		before := len(f.recorded())
		_, err := invoke(t, core, call.tool, call.connection, call.arguments, true)
		var failure *provider.Error
		if !errors.As(err, &failure) || failure.Class != provider.ClassNotFound {
			t.Errorf("%s %s = %v, want not-found", call.tool, call.arguments, err)
		}
		for _, request := range sent(f, before) {
			if request.method != http.MethodGet {
				t.Errorf("%s %s sent %s %s", call.tool, call.arguments, request.method, request.path)
			}
		}
	}
	// A thread of an owner target's repository is admitted.
	if _, err := invoke(t, core, notificationsGet.ID, "org", `{"thread_id":"300"}`, false); err != nil {
		t.Errorf("thread of the owner target = %v", err)
	}
}

// A fine-grained token is refused with a permission failure before any request.
func TestNotificationsRefuseAFineGrainedToken(t *testing.T) {
	f := &fakeGitHub{}
	f.failure = (&fakeNotifications{title: "t", f: f}).route
	base := serve(t, f)
	const fineGrained = "github_pat_canaryFineGrainedToken0123456789"
	t.Cleanup(limiters.Replace(fineGrained, freeLimiter()))
	red := &redact.Redactor{}
	secrets := secret.NewWith(func(name string) string {
		if name == tokenEnv {
			return fineGrained
		}
		return ""
	}, nil, nil, red)
	cfg := notificationsConfig(base)
	core := application.New(registry(t), cfg, secrets, red)
	for _, call := range []struct{ tool, arguments string }{
		{notificationsList.ID, `{}`},
		{notificationsGet.ID, `{"thread_id":"100"}`},
		{notificationsDismiss.ID, `{"thread_id":"100","state":"read"}`},
		{threadSubscriptionsSet.ID, `{"thread_id":"100","action":"watch"}`},
	} {
		_, err := invoke(t, core, call.tool, "open", call.arguments, true)
		var failure *provider.Error
		if !errors.As(err, &failure) || failure.Class != provider.ClassPermission ||
			!strings.Contains(failure.Message, "personal access token (classic)") ||
			strings.Contains(err.Error(), fineGrained) {
			t.Errorf("%s with a fine-grained token = %v, want a permission failure", call.tool, err)
		}
	}
	if _, err := invoke(t, core, notificationsMarkAll.ID, "open-listed", `{}`, true); err == nil ||
		!strings.Contains(err.Error(), "fine-grained") {
		t.Errorf("mark all with a fine-grained token = %v, want a permission failure", err)
	}
	if len(f.recorded()) != 0 {
		t.Errorf("a fine-grained token reached GitHub: %+v", f.recorded())
	}
}

// GitHub's refusals of a classic token without the scope name what the notification tools need.
func TestNotificationRefusalsNameTheRequiredToken(t *testing.T) {
	f := &fakeGitHub{}
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.Contains(r.URL.Path, "/notifications") {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"Resource not accessible by personal access token"}`)
			return true
		}
		return false
	}
	red := &redact.Redactor{}
	core := application.New(registry(t), notificationsConfig(serve(t, f)), resolver(red, nil), red)
	for _, call := range []struct{ tool, arguments string }{
		{notificationsList.ID, `{}`}, {notificationsGet.ID, `{"thread_id":"100"}`},
		{notificationsDismiss.ID, `{"thread_id":"100","state":"done"}`},
	} {
		_, err := invoke(t, core, call.tool, "open", call.arguments, true)
		var failure *provider.Error
		if !errors.As(err, &failure) || failure.Class != provider.ClassPermission ||
			!strings.Contains(failure.Message, "notifications scope") {
			t.Errorf("%s refused = %v, want the notifications scope named", call.tool, err)
		}
	}
	// Not-found names the thread, never a value GitHub sent.
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusNotFound)
		return true
	}
	_, err := invoke(t, core, notificationsGet.ID, "open", `{"thread_id":"100"}`, false)
	if err == nil || !strings.Contains(err.Error(), "this notification thread") {
		t.Errorf("unknown thread = %v", err)
	}
	client := client(t, serve(t, f), "")
	if _, err := client.listNotifications(context.Background(), &notificationListOptions{}, target{}, false); err == nil {
		t.Error("an unknown route answered")
	}
}

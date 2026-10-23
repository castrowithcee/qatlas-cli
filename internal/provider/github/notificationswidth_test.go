package github

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// documentedThread is the thread of GitHub's REST reference with the full repository object.
const documentedThread = `{"id":"1","repository":{"id":1296269,"node_id":"MDEwOlJlcG9zaXRvcnkxMjk2MjY5",` +
	`"name":"Hello-World","full_name":"octo-org/example","owner":` + documentedAccount + `,"private":false,` +
	`"html_url":"https://github.com/octo-org/example","description":"This your first repo!","fork":false,` +
	`"url":"https://api.github.com/repos/octo-org/example","archive_url":"https://api.github.com/repos/octo-org/example/{archive_format}{/ref}",` +
	`"temp_clone_token":"secret-token"},"subject":{"title":"Greetings","url":"https://api.github.com/repos/octo-org/example/issues/1",` +
	`"latest_comment_url":"https://api.github.com/repos/octo-org/example/issues/comments/1","type":"Issue"},` +
	`"reason":"subscribed","unread":true,"updated_at":"2014-11-07T22:01:45Z","last_read_at":"2014-11-07T22:01:45Z",` +
	`"url":"https://api.github.com/notifications/threads/1",` +
	`"subscription_url":"https://api.github.com/notifications/threads/1/subscription"}`

const threadWant = `"id":"1","repository":"octo-org/example","reason":"subscribed","unread":true,` +
	`"updated_at":"2014-11-07T22:01:45Z","last_read_at":"2014-11-07T22:01:45Z","subject_title":"Greetings",` +
	`"subject_type":"Issue","subject_url":"https://api.github.com/repos/octo-org/example/issues/1",` +
	`"latest_comment_url":"https://api.github.com/repos/octo-org/example/issues/comments/1",` +
	`"links":{"url":"https://api.github.com/notifications/threads/1",` +
	`"subscription_url":"https://api.github.com/notifications/threads/1/subscription"}`

const threadRepositoryWant = `{"id":1296269,"node_id":"MDEwOlJlcG9zaXRvcnkxMjk2MjY5","name":"Hello-World",` +
	`"full_name":"octo-org/example","owner":` + accountWant + `,"private":false,` +
	`"url":"https://github.com/octo-org/example","description":"This your first repo!","fork":false,` +
	`"links":{"url":"https://api.github.com/repos/octo-org/example",` +
	`"archive_url":"https://api.github.com/repos/octo-org/example/{archive_format}{/ref}"}}`

func notificationWidthCore(t *testing.T, thread string) *application.Core {
	t.Helper()
	_, base := serveRoutes(t, map[string]string{"/api/v3/notifications": "[" + thread + "]",
		"/api/v3/notifications/threads/1": thread})
	red := &redact.Redactor{}
	return application.New(registry(t), notificationsConfig(base), resolver(red, nil), red)
}

// The notification tools carry the repository object and the addresses of GitHub's thread; the clone token
// never leaves, and a repository outside the targets keeps only its identifiers.
func TestNotificationsCarryTheRepositoryObjectAndAddresses(t *testing.T) {
	core := notificationWidthCore(t, documentedThread)
	got, err := invoke(t, core, notificationsGet.ID, "open", `{"thread_id":"1"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "notifications.get", got, `{`+threadWant+`,"repository_detail":`+threadRepositoryWant+`}`)
	got, err = invoke(t, core, notificationsList.ID, "open", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "notifications.list", got, `{"notifications":[{`+threadWant+`,"repository_detail":`+threadRepositoryWant+`}],"has_more":false}`)
}

// A connection that names the owner reads the thread with its repository object; the object is the same
// where the targets admit the repository by its owner.
func TestNotificationsOfAnOwnerTargetCarryTheRepositoryObject(t *testing.T) {
	thread := strings.Replace(documentedThread, `"owner":`+documentedAccount, `"owner":{"login":"octo-org"}`, 1)
	core := notificationWidthCore(t, thread)
	got, err := invoke(t, core, notificationsList.ID, "org", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(threadRepositoryWant, `"owner":`+accountWant, `"owner":{"login":"octo-org"}`, 1)
	sameJSON(t, "notifications.list org", got, `{"notifications":[{`+threadWant+`,"repository_detail":`+want+`}],"has_more":false}`)
}

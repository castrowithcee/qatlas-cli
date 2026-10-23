package github

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// documentedGistOrigin is the fork_of object of GitHub's REST reference, with every member set.
const documentedGistOrigin = `{"url":"https://api.github.com/gists/bb11","forks_url":"https://api.github.com/gists/bb11/forks",` +
	`"commits_url":"https://api.github.com/gists/bb11/commits","id":"bb11","node_id":"G_bb11",` +
	`"git_pull_url":"https://gist.github.com/bb11.git","git_push_url":"https://gist.github.com/bb11.git",` +
	`"html_url":"https://gist.github.com/bb11","files":{"a.rb":{"filename":"a.rb","raw_url":"https://gist.example.com/raw/a.rb"}},` +
	`"public":true,"created_at":"2010-04-14T02:15:15Z","updated_at":"2011-06-20T11:34:15Z","description":"Original",` +
	`"comments":3,"comments_enabled":false,"user":null,"comments_url":"https://api.github.com/gists/bb11/comments",` +
	`"owner":` + documentedAccount + `,"truncated":false}`

const documentedGistFile = `{"filename":"hello_world.rb","type":"application/x-ruby","language":"Ruby",` +
	`"raw_url":"https://gist.githubusercontent.com/octocat/6cad326836d38bd3a7ae/raw/hello_world.rb","size":167,` +
	`"truncated":false,"content":"puts 1","encoding":"utf-8"}`

func documentedGist(public bool, extra string) string {
	return `{"url":"https://api.github.com/gists/aa5a315d61ae9438b18d",` +
		`"forks_url":"https://api.github.com/gists/aa5a315d61ae9438b18d/forks",` +
		`"commits_url":"https://api.github.com/gists/aa5a315d61ae9438b18d/commits","id":"aa5a315d61ae9438b18d",` +
		`"node_id":"MDQ6R2lzdGFhNWEzMTVkNjFhZTk0MzhiMThk","git_pull_url":"https://gist.github.com/aa5a315d61ae9438b18d.git",` +
		`"git_push_url":"https://gist.github.com/aa5a315d61ae9438b18d.git","html_url":"https://gist.github.com/aa5a315d61ae9438b18d",` +
		`"files":{"hello_world.rb":` + documentedGistFile + `},"public":` + fmt.Sprint(public) + `,` +
		`"created_at":"2010-04-14T02:35:02Z","updated_at":"2011-06-20T11:34:15Z","description":"Hello World Examples",` +
		`"comments":1,"comments_enabled":true,"user":null,"comments_url":"https://api.github.com/gists/aa5a315d61ae9438b18d/comments",` +
		`"owner":` + documentedAccount + `,"truncated":false` + extra + `}`
}

const gistWideWant = `"node_id":"MDQ6R2lzdGFhNWEzMTVkNjFhZTk0MzhiMThk","comments":1,"comments_enabled":true,` +
	`"owner_detail":` + accountWant + `,"truncated":false,"links":{"url":"https://api.github.com/gists/aa5a315d61ae9438b18d",` +
	`"forks_url":"https://api.github.com/gists/aa5a315d61ae9438b18d/forks",` +
	`"commits_url":"https://api.github.com/gists/aa5a315d61ae9438b18d/commits",` +
	`"comments_url":"https://api.github.com/gists/aa5a315d61ae9438b18d/comments",` +
	`"git_pull_url":"https://gist.github.com/aa5a315d61ae9438b18d.git",` +
	`"git_push_url":"https://gist.github.com/aa5a315d61ae9438b18d.git"}`

const gistOriginWant = `{"id":"bb11","node_id":"G_bb11","description":"Original","public":true,` +
	`"created_at":"2010-04-14T02:15:15Z","updated_at":"2011-06-20T11:34:15Z","url":"https://gist.github.com/bb11",` +
	`"comments":3,"comments_enabled":false,"owner":` + accountWant + `,"truncated":false,` +
	`"links":{"url":"https://api.github.com/gists/bb11","forks_url":"https://api.github.com/gists/bb11/forks",` +
	`"commits_url":"https://api.github.com/gists/bb11/commits","comments_url":"https://api.github.com/gists/bb11/comments",` +
	`"git_pull_url":"https://gist.github.com/bb11.git","git_push_url":"https://gist.github.com/bb11.git"}}`

func gistWidthCore(t *testing.T, routes map[string]string) *application.Core {
	t.Helper()
	_, base := serveRoutes(t, routes)
	red := &redact.Redactor{}
	return application.New(registry(t), notificationsConfig(base), resolver(red, nil), red)
}

func gistFileWant(content string) string {
	return `{"filename":"hello_world.rb","type":"application/x-ruby","language":"Ruby","size":167,` + content +
		`"encoding":"utf-8","links":{"raw_url":"https://gist.githubusercontent.com/octocat/6cad326836d38bd3a7ae/raw/hello_world.rb"}}`
}

func gistCompactWant(public bool, file string) string {
	return `"id":"aa5a315d61ae9438b18d","description":"Hello World Examples","public":` + fmt.Sprint(public) + `,` +
		`"owner":"octocat","created_at":"2010-04-14T02:35:02Z","updated_at":"2011-06-20T11:34:15Z",` +
		`"url":"https://gist.github.com/aa5a315d61ae9438b18d","file_count":1,"files":[` + file + `]`
}

// A single gist carries the members of GitHub's gist-simple except its deprecated lists, and a secret gist
// carries its id and addresses like a public one.
func TestGistGetCarriesTheFullGist(t *testing.T) {
	for _, public := range []bool{true, false} {
		deprecated := `,"forks":[{"id":"x"}],"history":[{"version":"v"}],"fork_of":` + documentedGistOrigin
		core := gistWidthCore(t, map[string]string{"/api/v3/gists/aa5a315d61ae9438b18d": documentedGist(public, deprecated)})
		got, err := invoke(t, core, gistsGet.ID, "open", `{"gist_id":"aa5a315d61ae9438b18d"}`, false)
		if err != nil {
			t.Fatal(err)
		}
		var shown any
		var schema map[string]any
		if json.Unmarshal(got, &shown) != nil || json.Unmarshal(gistsGet.OutputSchema, &schema) != nil {
			t.Fatal("unreadable answer or schema")
		}
		checkSchemaCovers(t, "$", shown, schema)
		sameJSON(t, fmt.Sprint("gists.get public=", public), got, `{`+gistCompactWant(public, gistFileWant(`"content":"puts 1",`))+`,`+
			gistWideWant+`,"fork_of":`+gistOriginWant+`}`)
	}
}

// A list carries the same members, with the forks and the history capped at ten each and marked.
func TestGistListCarriesForksAndHistoryCapped(t *testing.T) {
	var forks, history []string
	for i := 0; i < 12; i++ {
		forks = append(forks, fmt.Sprintf(`{"id":"f%d","url":"https://api.github.com/gists/f%d","user":%s,`+
			`"created_at":"2011-04-14T16:00:49Z","updated_at":"2011-04-14T16:00:49Z"}`, i, i, documentedAccount))
		history = append(history, fmt.Sprintf(`{"user":%s,"version":"v%d","committed_at":"2010-04-14T02:15:15Z",`+
			`"change_status":{"total":180,"additions":180,"deletions":0},"url":"https://api.github.com/gists/aa/v%d"}`,
			documentedAccount, i, i))
	}
	extra := `,"forks":[` + strings.Join(forks, ",") + `],"history":[` + strings.Join(history, ",") + `]`
	core := gistWidthCore(t, map[string]string{"/api/v3/gists": "[" + documentedGist(false, extra) + "]"})
	got, err := invoke(t, core, gistsList.ID, "open", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var wantForks, wantHistory []string
	for i := 0; i < 10; i++ {
		wantForks = append(wantForks, fmt.Sprintf(`{"id":"f%d","user":%s,"created_at":"2011-04-14T16:00:49Z",`+
			`"updated_at":"2011-04-14T16:00:49Z","links":{"url":"https://api.github.com/gists/f%d"}}`, i, accountWant, i))
		wantHistory = append(wantHistory, fmt.Sprintf(`{"version":"v%d","user":%s,"committed_at":"2010-04-14T02:15:15Z",`+
			`"change_status":{"total":180,"additions":180,"deletions":0},"links":{"url":"https://api.github.com/gists/aa/v%d"}}`,
			i, accountWant, i))
	}
	sameJSON(t, "gists.list", got, `{"gists":[{`+gistCompactWant(false, gistFileWant(""))+`,`+gistWideWant+`,`+
		`"forks":[`+strings.Join(wantForks, ",")+`],"forks_truncated":true,`+
		`"history":[`+strings.Join(wantHistory, ",")+`],"history_truncated":true}],"has_more":false}`)

	var first map[string]any
	if err := json.Unmarshal(got, &first); err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(gistsList.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	checkSchemaCovers(t, "$", first, schema)
}

// A gist without the new members keeps its earlier output; a user member that is no account stays out.
func TestGistWithoutNewMembersKeepsItsOldAnswer(t *testing.T) {
	const old = `{"id":"aa11","description":"d","public":true,"created_at":"c","updated_at":"u",` +
		`"html_url":"https://gist.github.com/aa11","user":"octocat","owner":{"login":"octocat"},` +
		`"files":{"a.txt":{"filename":"a.txt","type":"text/plain","size":3,"content":"hey"}}}`
	core := gistWidthCore(t, map[string]string{"/api/v3/gists/aa11": old})
	got, err := invoke(t, core, gistsGet.ID, "open", `{"gist_id":"aa11"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "gists.get", got, `{"id":"aa11","description":"d","public":true,"owner":"octocat","created_at":"c",`+
		`"updated_at":"u","url":"https://gist.github.com/aa11","file_count":1,"files":[{"filename":"a.txt",`+
		`"type":"text/plain","size":3,"content":"hey"}],"owner_detail":{"login":"octocat"}}`)
}

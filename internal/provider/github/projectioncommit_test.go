package github

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// The commit documented by GitHub's REST reference for "List commits on a pull request", completed with the
// members the example leaves out (html_url of the commit and its parent, the stats, a file, and a signed
// verification).
const (
	documentedCommitSHA = "6dcb09b5b57875f334f61aebed695e2e4193db5e"
	documentedCommitAPI = "https://api.github.com/repos/octocat/Hello-World/commits/" + documentedCommitSHA
	documentedGitUser   = `{"name":"Monalisa Octocat","email":"octocat@example.invalid","date":"2011-04-14T16:00:49Z"}`
	documentedDiffEntry = `{"sha":"bbcd538c8e72b8c175046e27cc8f907076331401","filename":"file1.txt","status":"added",` +
		`"additions":103,"deletions":21,"changes":124,` +
		`"blob_url":"https://github.com/octocat/Hello-World/blob/` + documentedCommitSHA + `/file1.txt",` +
		`"raw_url":"https://github.com/octocat/Hello-World/raw/` + documentedCommitSHA + `/file1.txt",` +
		`"contents_url":"https://api.github.com/repos/octocat/Hello-World/contents/file1.txt?ref=` + documentedCommitSHA + `",` +
		`"patch":"@@ -132,7 +132,7 @@ module Test @@ -1000,7 +1000,7 @@ module Test"}`
	documentedCommit = `{"url":"` + documentedCommitAPI + `","sha":"` + documentedCommitSHA + `",` +
		`"node_id":"MDY6Q29tbWl0NmRjYjA5YjViNTc4NzVmMzM0ZjYxYWViZWQ2OTVlMmU0MTkzZGI1ZQ==",` +
		`"html_url":"https://github.com/octocat/Hello-World/commit/` + documentedCommitSHA + `",` +
		`"comments_url":"` + documentedCommitAPI + `/comments",` +
		`"commit":{"url":"https://api.github.com/repos/octocat/Hello-World/git/commits/` + documentedCommitSHA + `",` +
		`"author":` + documentedGitUser + `,"committer":` + documentedGitUser + `,"message":"Fix all the bugs",` +
		`"tree":{"url":"https://api.github.com/repos/octocat/Hello-World/tree/` + documentedCommitSHA + `",` +
		`"sha":"` + documentedCommitSHA + `"},"comment_count":2,` +
		`"verification":{"verified":true,"reason":"valid","signature":"-----BEGIN PGP SIGNATURE-----\nabc\n",` +
		`"payload":"tree x\nauthor y\n","verified_at":"2011-04-14T16:00:49Z"}},` +
		`"author":` + documentedAccount + `,"committer":{},` +
		`"parents":[{"url":"` + documentedCommitAPI + `","sha":"` + documentedCommitSHA + `",` +
		`"html_url":"https://github.com/octocat/Hello-World/commit/` + documentedCommitSHA + `"}],` +
		`"stats":{"additions":103,"deletions":0,"total":103},"files":[` + documentedDiffEntry + `]}`

	documentedCommitWant = `{"sha":"` + documentedCommitSHA + `",` +
		`"author":"octocat","committer":"Monalisa Octocat","message":"Fix all the bugs",` +
		`"date":"2011-04-14T16:00:49Z","parents":1,` +
		`"node_id":"MDY6Q29tbWl0NmRjYjA5YjViNTc4NzVmMzM0ZjYxYWViZWQ2OTVlMmU0MTkzZGI1ZQ==",` +
		`"url":"https://github.com/octocat/Hello-World/commit/` + documentedCommitSHA + `",` +
		`"links":{"url":"` + documentedCommitAPI + `","comments_url":"` + documentedCommitAPI + `/comments"},` +
		`"author_detail":` + accountWant + `,` +
		`"commit":{"message":"Fix all the bugs","comment_count":2,` +
		`"author":` + documentedGitUser + `,"committer":` + documentedGitUser + `,` +
		`"tree":{"sha":"` + documentedCommitSHA + `","links":{"url":` +
		`"https://api.github.com/repos/octocat/Hello-World/tree/` + documentedCommitSHA + `"}},` +
		`"verification":{"verified":true,"reason":"valid","verified_at":"2011-04-14T16:00:49Z",` +
		`"payload":"tree x\nauthor y\n","signature":"-----BEGIN PGP SIGNATURE-----\nabc\n"},` +
		`"links":{"url":"https://api.github.com/repos/octocat/Hello-World/git/commits/` + documentedCommitSHA + `"}},` +
		`"parents_details":[{"sha":"` + documentedCommitSHA + `",` +
		`"url":"https://github.com/octocat/Hello-World/commit/` + documentedCommitSHA + `",` +
		`"links":{"url":"` + documentedCommitAPI + `"}}],` +
		`"stats":{"additions":103,"deletions":0,"total":103},` +
		`"files":[{"sha":"bbcd538c8e72b8c175046e27cc8f907076331401","path":"file1.txt","status":"added",` +
		`"additions":103,"deletions":21,"changes":124,` +
		`"patch":"@@ -132,7 +132,7 @@ module Test @@ -1000,7 +1000,7 @@ module Test",` +
		`"links":` + documentedFileLinks + `}]}`
	documentedFileLinks = `{"blob_url":"https://github.com/octocat/Hello-World/blob/` + documentedCommitSHA + `/file1.txt",` +
		`"raw_url":"https://github.com/octocat/Hello-World/raw/` + documentedCommitSHA + `/file1.txt",` +
		`"contents_url":"https://api.github.com/repos/octocat/Hello-World/contents/file1.txt?ref=` + documentedCommitSHA + `"}`
)

func pullListCore(t *testing.T, suffix, body string) *application.Core {
	t.Helper()
	_, base := serveRoutes(t, map[string]string{"/api/v3/repos/octo-org/example/pulls/100/" + suffix: body})
	red := &redact.Redactor{}
	return application.New(registry(t), coreConfig(base), resolver(red, nil), red)
}

func TestPullRequestCommitsReportEveryDocumentedMember(t *testing.T) {
	core := pullListCore(t, "commits", `[`+documentedCommit+`]`)
	got, err := invoke(t, core, "github.pull-request-commits.list", "repo", `{"number":100}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "commits", got, `{"commits":[`+documentedCommitWant+`],"has_more":false,"repository":"octo-org/example"}`)
}

// The GitHub example itself carries no stats, files, or html_url; the flat members and the new members that
// the example does hold are reported, and nothing is invented for the rest.
func TestPullRequestCommitsOfTheDocumentationExample(t *testing.T) {
	example := `[{"url":"` + documentedCommitAPI + `","sha":"` + documentedCommitSHA + `",` +
		`"node_id":"N","html_url":"https://github.com/octocat/Hello-World/commit/` + documentedCommitSHA + `",` +
		`"comments_url":"` + documentedCommitAPI + `/comments","commit":{"url":"u","author":` + documentedGitUser +
		`,"committer":` + documentedGitUser + `,"message":"Fix all the bugs","tree":{"url":"t","sha":"s"},` +
		`"comment_count":0,"verification":{"verified":false,"reason":"unsigned","signature":null,"payload":null,` +
		`"verified_at":null}},"author":null,"committer":{},"parents":[{"url":"p","sha":"q"}]}]`
	core := pullListCore(t, "commits", example)
	got, err := invoke(t, core, "github.pull-request-commits.list", "repo", `{"number":100}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "commits", got, `{"commits":[{"sha":"`+documentedCommitSHA+`","author":"Monalisa Octocat",`+
		`"committer":"Monalisa Octocat","message":"Fix all the bugs","date":"2011-04-14T16:00:49Z","parents":1,`+
		`"node_id":"N","url":"https://github.com/octocat/Hello-World/commit/`+documentedCommitSHA+`",`+
		`"links":{"url":"`+documentedCommitAPI+`","comments_url":"`+documentedCommitAPI+`/comments"},`+
		`"commit":{"message":"Fix all the bugs","comment_count":0,"author":`+documentedGitUser+`,"committer":`+
		documentedGitUser+`,"tree":{"sha":"s","links":{"url":"t"}},"verification":{"verified":false,`+
		`"reason":"unsigned"},"links":{"url":"u"}},"parents_details":[{"sha":"q","links":{"url":"p"}}]}],`+
		`"has_more":false,"repository":"octo-org/example"}`)
}

// A commit without the new members keeps its earlier answer.
func TestPullRequestCommitsWithoutNewMembersKeepTheirOldAnswer(t *testing.T) {
	core := pullListCore(t, "commits", `[{"sha":"sha1","commit":{"author":{"name":"A","date":"2026-01-01T00:00:00Z"},`+
		`"message":"m"},"author":{"login":"octocat"},"html_url":"https://github.com/o/r/commit/sha1"}]`)
	got, err := invoke(t, core, "github.pull-request-commits.list", "repo", `{"number":100}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "commits", got, `{"commits":[{"sha":"sha1","author":"octocat","message":"m",`+
		`"date":"2026-01-01T00:00:00Z","url":"https://github.com/o/r/commit/sha1",`+
		`"author_detail":{"login":"octocat"},`+
		`"commit":{"message":"m","author":{"name":"A","date":"2026-01-01T00:00:00Z"}}}],`+
		`"has_more":false,"repository":"octo-org/example"}`)
}

// Payload, signature, and message are untrusted and cut with a marker.
func TestPullRequestCommitsBoundTheirTexts(t *testing.T) {
	long := strings.Repeat("x", maxCommitMessageLength+1)
	raw, _ := json.Marshal([]map[string]any{{"sha": "sha1", "commit": map[string]any{"message": long + "\x00",
		"verification": map[string]any{"payload": long, "signature": long}}}})
	core := pullListCore(t, "commits", string(raw))
	got, err := invoke(t, core, "github.pull-request-commits.list", "repo", `{"number":100}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var listed PullRequestCommitList
	if err := json.Unmarshal(got, &listed); err != nil || len(listed.Commits) != 1 {
		t.Fatalf("commits = %s, %v", got, err)
	}
	c := listed.Commits[0]
	if len(c.Message) != maxCommitMessageLength || !c.MessageTruncated || !c.Commit.MessageTruncated ||
		len(*c.Commit.Verification.Payload) != projectionSignatureLimit || !c.Commit.Verification.PayloadTruncated ||
		len(*c.Commit.Verification.Signature) != projectionSignatureLimit || !c.Commit.Verification.SignatureTruncated {
		t.Errorf("commit = %s", got)
	}
}

func TestPullRequestFilesReportBlobSHAAndLinks(t *testing.T) {
	core := pullListCore(t, "files", `[`+documentedDiffEntry+`]`)
	got, err := invoke(t, core, "github.pull-request-files.list", "repo", `{"number":100}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "files", got, `{"files":[{"path":"file1.txt","status":"added","additions":103,"deletions":21,`+
		`"changes":124,"patch":"@@ -132,7 +132,7 @@ module Test @@ -1000,7 +1000,7 @@ module Test",`+
		`"sha":"bbcd538c8e72b8c175046e27cc8f907076331401","links":`+documentedFileLinks+`}],"has_more":false,"repository":"octo-org/example"}`)

	core = pullListCore(t, "files", `[{"filename":"a.go","status":"modified","additions":1,"deletions":0,"changes":1,`+
		`"sha":null}]`)
	got, err = invoke(t, core, "github.pull-request-files.list", "repo", `{"number":100}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "files", got, `{"files":[{"path":"a.go","status":"modified","additions":1,"deletions":0,"changes":1}],`+
		`"has_more":false,"repository":"octo-org/example"}`)
}

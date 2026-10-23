package github

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// The fixture address is assembled so the source holds no literal e-mail address.
const monaEmail = "mona" + "@" + "github.com"

// The commit documented by GitHub's REST reference for "Get a commit", its accounts shortened to
// documentedAccount; the verification carries a signature to check the cut.
const (
	documentedCommitInner = `"commit":{"url":"https://api.github.com/repos/octo-org/example/git/commits/6dcb09b5",` +
		`"author":{"name":"Monalisa Octocat","email":"` + monaEmail + `","date":"2011-04-14T16:00:49Z"},` +
		`"committer":{"name":"Monalisa Octocat","email":"` + monaEmail + `","date":"2011-04-14T16:00:49Z"},` +
		`"message":"Fix all the bugs\n\nWith details.","tree":{"url":"https://api.github.com/repos/octo-org/example/git/trees/6dcb09b5",` +
		`"sha":"6dcb09b5"},"comment_count":2,"verification":{"verified":true,"reason":"valid",` +
		`"signature":"-----BEGIN PGP SIGNATURE-----","payload":"tree 6dcb09b5","verified_at":"2011-04-14T16:00:50Z"}}`
	documentedCommitHead = `"url":"https://api.github.com/repos/octo-org/example/commits/6dcb09b5",` +
		`"sha":"6dcb09b5","node_id":"MDY6Q29tbWl0NmRjYjA5YjU=",` +
		`"html_url":"https://github.com/octo-org/example/commit/6dcb09b5",` +
		`"comments_url":"https://api.github.com/repos/octo-org/example/commits/6dcb09b5/comments",` +
		documentedCommitInner + `,"author":` + documentedAccount + `,"committer":` + documentedAccount + `,` +
		`"parents":[{"url":"https://api.github.com/repos/octo-org/example/commits/6dcb09b4",` +
		`"html_url":"https://github.com/octo-org/example/commit/6dcb09b4","sha":"6dcb09b4"}]`
	documentedCommitList = `{` + documentedCommitHead + `}`
	documentedCommitGet  = `{` + documentedCommitHead + `,"stats":{"additions":104,"deletions":4,"total":108},` +
		`"files":[{"sha":"bbcd538c","filename":"file1.txt","status":"added","additions":103,"deletions":21,` +
		`"changes":124,"blob_url":"https://github.com/octo-org/example/blob/6dcb09b5/file1.txt",` +
		`"raw_url":"https://github.com/octo-org/example/raw/6dcb09b5/file1.txt",` +
		`"contents_url":"https://api.github.com/repos/octo-org/example/contents/file1.txt?ref=6dcb09b5",` +
		`"patch":"@@ -132,7 +132,7 @@"}]}`
)

// commitWidthWant holds every member the commit width adds, the same in all three commit reads.
func commitWidthWant() map[string]string {
	return map[string]string{
		"node_id": `"MDY6Q29tbWl0NmRjYjA5YjU="`,
		"url":     `"https://github.com/octo-org/example/commit/6dcb09b5"`,
		"links": `{"url":"https://api.github.com/repos/octo-org/example/commits/6dcb09b5",` +
			`"comments_url":"https://api.github.com/repos/octo-org/example/commits/6dcb09b5/comments"}`,
		"author_detail": accountWant, "committer_detail": accountWant,
		"commit": `{"message":"Fix all the bugs\n\nWith details.","comment_count":2,` +
			`"author":{"name":"Monalisa Octocat","email":"` + monaEmail + `","date":"2011-04-14T16:00:49Z"},` +
			`"committer":{"name":"Monalisa Octocat","email":"` + monaEmail + `","date":"2011-04-14T16:00:49Z"},` +
			`"tree":{"sha":"6dcb09b5","links":{"url":"https://api.github.com/repos/octo-org/example/git/trees/6dcb09b5"}},` +
			`"verification":{"verified":true,"reason":"valid","verified_at":"2011-04-14T16:00:50Z",` +
			`"payload":"tree 6dcb09b5","signature":"-----BEGIN PGP SIGNATURE-----"},` +
			`"links":{"url":"https://api.github.com/repos/octo-org/example/git/commits/6dcb09b5"}}`,
		"parents_details": `[{"sha":"6dcb09b4","url":"https://github.com/octo-org/example/commit/6dcb09b4",` +
			`"links":{"url":"https://api.github.com/repos/octo-org/example/commits/6dcb09b4"}}]`,
	}
}

// github.commits.get reports every documented member, keeps the flat members, and adds the sha and the
// addresses of each file.
func TestCommitGetReportsEveryDocumentedMember(t *testing.T) {
	f, core := cannedCore(t, documentedCommitGet, false, discoveryConfig)
	got, err := invoke(t, core, commitsGet.ID, "repo", `{"ref":"6dcb09b5"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if requests := f.recorded(); len(requests) != 1 {
		t.Errorf("requests = %d, want one", len(requests))
	}
	object := members(t, got)
	wantMembers(t, "get", object, commitWidthWant())
	wantMembers(t, "get", object, map[string]string{
		"sha": `"6dcb09b5"`, "message": `"Fix all the bugs\n\nWith details."`, "author": `"octocat"`,
		"committer": `"octocat"`, "date": `"2011-04-14T16:00:49Z"`, "parents": `["6dcb09b4"]`,
		"additions": `104`, "deletions": `4`, "total": `108`, "files_truncated": `false`,
		"files": `[{"path":"file1.txt","status":"added","additions":103,"deletions":21,"changes":124,` +
			`"patch":"@@ -132,7 +132,7 @@","sha":"bbcd538c","links":{` +
			`"blob_url":"https://github.com/octo-org/example/blob/6dcb09b5/file1.txt",` +
			`"raw_url":"https://github.com/octo-org/example/raw/6dcb09b5/file1.txt",` +
			`"contents_url":"https://api.github.com/repos/octo-org/example/contents/file1.txt?ref=6dcb09b5"}}]`,
	})
	if _, ok := object["stats"]; ok {
		t.Errorf("the answer holds stats, which additions, deletions, and total cover")
	}
}

// github.commits.list reports the same width for each entry, without stats and files.
func TestCommitListReportsEveryDocumentedMember(t *testing.T) {
	_, core := cannedCore(t, `[`+documentedCommitList+`]`, false, discoveryConfig)
	got, err := invoke(t, core, commitsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	object := members(t, got, "commits", "#0")
	wantMembers(t, "list", object, commitWidthWant())
	wantMembers(t, "list", object, map[string]string{
		"sha": `"6dcb09b5"`, "message": `"Fix all the bugs"`, "author": `"octocat"`, "committer": `"octocat"`,
		"date": `"2011-04-14T16:00:49Z"`, "parents": `1`,
	})
	for _, absent := range []string{"stats", "files"} {
		if _, ok := object[absent]; ok {
			t.Errorf("a list entry holds %s", absent)
		}
	}
}

// A signature or payload over the bound is cut and marked.
func TestCommitVerificationIsCutAndMarked(t *testing.T) {
	long := strings.Repeat("s", projectionSignatureLimit+10)
	body := strings.Replace(documentedCommitList, `"signature":"-----BEGIN PGP SIGNATURE-----"`, `"signature":"`+long+`"`, 1)
	_, core := cannedCore(t, `[`+body+`]`, false, discoveryConfig)
	got, err := invoke(t, core, commitsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Commits []struct {
			Commit struct {
				Verification struct {
					Signature          string `json:"signature"`
					SignatureTruncated bool   `json:"signature_truncated"`
				} `json:"verification"`
			} `json:"commit"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(got, &page); err != nil || len(page.Commits) != 1 {
		t.Fatalf("answer = %s, %v", got, err)
	}
	v := page.Commits[0].Commit.Verification
	if len(v.Signature) != projectionSignatureLimit || !v.SignatureTruncated {
		t.Errorf("signature = %d bytes, truncated %v", len(v.Signature), v.SignatureTruncated)
	}
}

// A commit GitHub sends without the new members answers what it always did, plus the git commit it does send.
func TestCommitReadsWithoutNewMembersKeepTheirOldAnswer(t *testing.T) {
	const minimal = `{"sha":"abc","commit":{"message":"Fix\n\nBody","author":{"name":"A","date":"2026-01-01T00:00:00Z"},` +
		`"committer":{"name":"B","date":"2026-01-02T00:00:00Z"}},"author":null,"committer":null,"parents":[{"sha":"p1"}]}`
	_, core := cannedCore(t, `[`+minimal+`]`, false, discoveryConfig)
	got, err := invoke(t, core, commitsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "list", got, `{"has_more":false,"repository":"octo-org/example","commits":[{"sha":"abc","message":"Fix","author":"A",`+
		`"committer":"B","date":"2026-01-02T00:00:00Z","parents":1,`+
		`"commit":{"message":"Fix\n\nBody","author":{"name":"A","date":"2026-01-01T00:00:00Z"},`+
		`"committer":{"name":"B","date":"2026-01-02T00:00:00Z"}},"parents_details":[{"sha":"p1"}]}]}`)
}

// The committer filter reaches GitHub, binds the cursor only when set, and is checked before any request.
func TestCommitListCommitterReachesTheRequestAndTheCursor(t *testing.T) {
	f, core := cannedCore(t, `[`+documentedCommitList+`]`, true, discoveryConfig)
	plain, err := invoke(t, core, commitsList.ID, "repo", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if values, _ := url.ParseQuery(lastRequest(t, f).query); values.Has("committer") {
		t.Errorf("plain request carries a committer: %q", lastRequest(t, f).query)
	}
	first, err := invoke(t, core, commitsList.ID, "repo", `{"committer":"mona@example.invalid"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if values, _ := url.ParseQuery(lastRequest(t, f).query); values.Get("committer") != "mona@example.invalid" {
		t.Errorf("request = %q, want the committer", lastRequest(t, f).query)
	}
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(first, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %s, %v", first, err)
	}
	if _, err := invoke(t, core, commitsList.ID, "repo",
		`{"committer":"mona@example.invalid","cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Errorf("continuation with the same committer = %v", err)
	}
	before := len(f.recorded())
	for name, arguments := range map[string]string{
		"another committer": `{"committer":"hubot","cursor":"%s"}`,
		"no committer":      `{"cursor":"%s"}`,
	} {
		if _, err := invoke(t, core, commitsList.ID, "repo", fmt.Sprintf(arguments, page.NextCursor), false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want a refused cursor", name, err)
		}
	}
	// A cursor issued without the committer stays valid without it and is refused with one.
	if err := json.Unmarshal(plain, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("plain page = %s, %v", plain, err)
	}
	if _, err := invoke(t, core, commitsList.ID, "repo", `{"cursor":"`+page.NextCursor+`"}`, false); err != nil {
		t.Errorf("plain continuation = %v", err)
	}
	before = len(f.recorded())
	for _, arguments := range []string{`{"committer":"two words"}`, `{"committer":"a@b@c"}`, `{"committer":"@x"}`,
		`{"committer":"` + strings.Repeat("a", maxCommitterLength) + `@x"}`, `{"committer":"bad\nline"}`} {
		if _, err := invoke(t, core, commitsList.ID, "repo", arguments, false); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if len(f.recorded()) != before {
		t.Errorf("a refused committer reached GitHub")
	}
	if !validCommitter("dependabot[bot]") || !validCommitter(strings.Repeat("a", maxCommitterLength-2)+"@b") {
		t.Errorf("a bot login or an email address of the bound was refused")
	}
}

// The commit search item documented by GitHub's REST reference, with the text match example.
const documentedCommitSearchPage = `{"total_count":1,"incomplete_results":false,"items":[` +
	`{"url":"https://api.github.com/repos/octo-org/example/commits/6dcb09b5",` +
	`"sha":"6dcb09b5","html_url":"https://github.com/octo-org/example/commit/6dcb09b5",` +
	`"comments_url":"https://api.github.com/repos/octo-org/example/commits/6dcb09b5/comments",` +
	`"commit":{"url":"https://api.github.com/repos/octo-org/example/git/commits/6dcb09b5",` +
	`"author":{"date":"2011-04-14T16:00:49Z","name":"Monalisa Octocat","email":"` + monaEmail + `"},` +
	`"committer":{"date":"2011-04-14T16:00:49Z","name":"Monalisa Octocat","email":"` + monaEmail + `"},` +
	`"message":"Fix all the bugs","tree":{"url":"https://api.github.com/repos/octo-org/example/git/trees/6dcb09b5",` +
	`"sha":"6dcb09b5"},"comment_count":0,"verification":{"verified":false,"reason":"unsigned",` +
	`"signature":null,"payload":null,"verified_at":null}},"author":` + documentedAccount + `,` +
	`"committer":` + documentedAccount + `,"parents":[{"url":"https://api.github.com/repos/octo-org/example/commits/6dcb09b4",` +
	`"html_url":"https://github.com/octo-org/example/commit/6dcb09b4","sha":"6dcb09b4"}],` +
	`"repository":` + pullBaseRepository + `,"score":1.5,"node_id":"MDY6Q29tbWl0NmRjYjA5YjU=",` +
	`"text_matches":[{"object_url":"https://api.github.com/repositories/99/commits/6dcb09b5",` +
	`"object_type":"Commit","property":"message","fragment":"Fix all the bugs",` +
	`"matches":[{"text":"bugs","indices":[12,16]}]}]}]}`

// github.commits.search reports every documented member, keeps the flat members, and asks for text matches,
// a sort, and an order only when told to.
func TestCommitSearchReportsEveryDocumentedMember(t *testing.T) {
	f, core := cannedCore(t, documentedCommitSearchPage, true, discoveryConfig)
	got, err := invoke(t, core, commitsSearch.ID, "repo", `{"terms":"bugs"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	request := lastRequest(t, f)
	values, _ := url.ParseQuery(request.query)
	if request.accept != "application/vnd.github+json" || values.Has("sort") || values.Has("order") {
		t.Errorf("plain request = %+v", request)
	}
	object := members(t, got, "commits", "#0")
	want := commitWidthWant()
	want["commit"] = `{"message":"Fix all the bugs","comment_count":0,` +
		`"author":{"name":"Monalisa Octocat","email":"` + monaEmail + `","date":"2011-04-14T16:00:49Z"},` +
		`"committer":{"name":"Monalisa Octocat","email":"` + monaEmail + `","date":"2011-04-14T16:00:49Z"},` +
		`"tree":{"sha":"6dcb09b5","links":{"url":"https://api.github.com/repos/octo-org/example/git/trees/6dcb09b5"}},` +
		`"verification":{"verified":false,"reason":"unsigned"},` +
		`"links":{"url":"https://api.github.com/repos/octo-org/example/git/commits/6dcb09b5"}}`
	wantMembers(t, "search", object, want)
	wantMembers(t, "search", object, map[string]string{
		"sha": `"6dcb09b5"`, "message": `"Fix all the bugs"`, "repository": `"octo-org/example"`,
		"author": `"Monalisa Octocat"`, "committed_at": `"2011-04-14T16:00:49Z"`, "committer": `"octocat"`,
		"parents": `1`, "score": `1.5`, "repository_detail": pullBaseRepositoryWant,
	})

	first, err := invoke(t, core, commitsSearch.ID, "repo",
		`{"terms":"bugs","text_matches":true,"sort":"committer-date","order":"asc"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	request = lastRequest(t, f)
	values, _ = url.ParseQuery(request.query)
	if request.accept != "application/vnd.github.text-match+json" || values.Get("sort") != "committer-date" ||
		values.Get("order") != "asc" {
		t.Errorf("request = %+v", request)
	}
	if !strings.Contains(values.Get("q"), "bugs") {
		t.Errorf("q = %q, want the terms kept in q and not the sort", values.Get("q"))
	}
	sameJSON(t, "text_matches", members(t, first, "commits", "#0")["text_matches"],
		`[{"object_type":"Commit","property":"message","fragment":"Fix all the bugs",`+
			`"matches":[{"text":"bugs","indices":[12,16]}],`+
			`"links":{"object_url":"https://api.github.com/repositories/99/commits/6dcb09b5"}}]`)

	// The filters bind the cursor.
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(first, &page); err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %s, %v", first, err)
	}
	const arguments = `{"terms":"bugs","text_matches":true,"sort":"committer-date","order":"asc","cursor":%q}`
	if _, err := invoke(t, core, commitsSearch.ID, "repo", fmt.Sprintf(arguments, page.NextCursor), false); err != nil {
		t.Errorf("continuation with the same filters = %v", err)
	}
	before := len(f.recorded())
	for name, other := range map[string]string{
		"another sort": `{"terms":"bugs","text_matches":true,"sort":"author-date","order":"asc","cursor":%q}`,
		"no order":     `{"terms":"bugs","text_matches":true,"sort":"committer-date","cursor":%q}`,
		"no matches":   `{"terms":"bugs","sort":"committer-date","order":"asc","cursor":%q}`,
		"no filters":   `{"terms":"bugs","cursor":%q}`,
	} {
		if _, err := invoke(t, core, commitsSearch.ID, "repo", fmt.Sprintf(other, page.NextCursor), false); !isInvalidRequest(err) {
			t.Errorf("%s = %v, want a refused cursor", name, err)
		}
	}
	for _, bad := range []string{`{"terms":"bugs","sort":"stars"}`, `{"terms":"bugs","order":"up"}`} {
		if _, err := invoke(t, core, commitsSearch.ID, "repo", bad, false); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
	if len(f.recorded()) != before {
		t.Errorf("a refused search reached GitHub")
	}
}

// A commit search match without the new members answers what it always did.
func TestCommitSearchWithoutNewMembersKeepsItsOldAnswer(t *testing.T) {
	const minimal = `{"total_count":1,"incomplete_results":false,"items":[{"sha":"abc",` +
		`"html_url":"https://github.com/octo-org/example/commit/abc","repository":{"full_name":"octo-org/example"},` +
		`"commit":{"message":"Fix","author":{"name":"A","date":"2026-01-01T00:00:00Z"}}}]}`
	_, core := cannedCore(t, minimal, false, discoveryConfig)
	got, err := invoke(t, core, commitsSearch.ID, "repo", `{"terms":"fix"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameJSON(t, "search", got, `{"commits":[{"sha":"abc","message":"Fix","repository":"octo-org/example",`+
		`"author":"A","committed_at":"2026-01-01T00:00:00Z","url":"https://github.com/octo-org/example/commit/abc",`+
		`"commit":{"message":"Fix","author":{"name":"A","date":"2026-01-01T00:00:00Z"}},`+
		`"repository_detail":{"full_name":"octo-org/example"}}],`+
		`"total_count":1,"incomplete_results":false,"has_more":false}`)
}

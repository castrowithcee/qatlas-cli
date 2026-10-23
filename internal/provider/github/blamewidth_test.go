package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const widthActor = `{"name":"Mona","email":"mona@example.com","date":"2026-03-01T10:00:00+01:00",` +
	`"avatarUrl":"https://avatars.example.com/u/1","user":` + widthUser + `}`

const widthActorView = `{"name":"Mona","email":"mona@example.com","date":"2026-03-01T10:00:00+01:00",` +
	`"user":` + widthUserView + `,"links":{"avatar_url":"https://avatars.example.com/u/1"}}`

const widthOrganization = `{"__typename":"Organization","id":"O_1","databaseId":9,"login":"octo-org","name":"Octo Org",` +
	`"email":"org@example.com","avatarUrl":"https://avatars.example.com/o/9","url":"https://github.com/octo-org",` +
	`"resourcePath":"/octo-org"}`

const widthOrganizationView = `{"type":"Organization","id":"O_1","database_id":9,"login":"octo-org","name":"Octo Org",` +
	`"email":"org@example.com","avatar_url":"https://avatars.example.com/o/9","url":"https://github.com/octo-org",` +
	`"links":{"resource_path":"/octo-org"}}`

// widthCommit is a Commit as the GraphQL schema describes it, with every member set.
func widthCommit(tarball string) string {
	return `{"id":"C_1","oid":"aaaa1111","abbreviatedOid":"aaaa111","messageHeadline":"Fix it",` +
		`"messageBody":"Because\nreasons","authoredDate":"2026-03-01T10:00:00+01:00",` +
		`"committedDate":"2026-03-01T11:00:00+01:00","authoredByCommitter":false,"committedViaWeb":true,` +
		`"additions":12,"deletions":0,"changedFilesIfAvailable":3,"author":` + widthActor + `,` +
		`"committer":` + widthActor + `,"onBehalfOf":` + widthOrganization + `,"signature":{"__typename":"GpgSignature",` +
		`"state":"VALID","isValid":true,"wasSignedByGitHub":false,"email":"mona@example.com","signer":` + widthUser + `,` +
		`"verifiedAt":"2026-03-01T12:00:00Z","payload":"tree abc\n","signature":"-----BEGIN PGP SIGNATURE-----",` +
		`"keyId":"ABCDEF0123456789"},"status":{"id":"S_1","state":"SUCCESS"},` +
		`"statusCheckRollup":{"id":"SCR_1","state":"PENDING"},"url":"https://github.com/octo-org/example/commit/aaaa1111",` +
		`"commitUrl":"https://github.com/octo-org/example/commit/aaaa1111","resourcePath":"/octo-org/example/commit/aaaa1111",` +
		`"commitResourcePath":"/octo-org/example/commit/aaaa1111","treeUrl":"https://github.com/octo-org/example/tree/aaaa1111",` +
		`"treeResourcePath":"/octo-org/example/tree/aaaa1111","tarballUrl":"` + tarball + `",` +
		`"zipballUrl":"` + tarball + `","tree":{"id":"T_1","oid":"tttt","abbreviatedOid":"ttt",` +
		`"commitUrl":"https://github.com/octo-org/example/commit/aaaa1111","commitResourcePath":"/octo-org/example/commit/aaaa1111"},` +
		`"viewerCanSubscribe":true,"viewerSubscription":"UNSUBSCRIBED"}`
}

const widthCommitView = `{"id":"C_1","oid":"aaaa1111","abbreviated_oid":"aaaa111","message_headline":"Fix it",` +
	`"message_body":"Because\nreasons","authored_date":"2026-03-01T10:00:00+01:00",` +
	`"committed_date":"2026-03-01T11:00:00+01:00","authored_by_committer":false,"committed_via_web":true,` +
	`"additions":12,"deletions":0,"changed_files_if_available":3,"author":` + widthActorView + `,` +
	`"committer":` + widthActorView + `,"on_behalf_of":` + widthOrganizationView + `,"signature":{"type":"GpgSignature",` +
	`"state":"VALID","is_valid":true,"was_signed_by_github":false,"email":"mona@example.com","signer":` + widthUserView + `,` +
	`"verified_at":"2026-03-01T12:00:00Z","payload":"tree abc\n","signature":"-----BEGIN PGP SIGNATURE-----",` +
	`"key_id":"ABCDEF0123456789"},"status":{"id":"S_1","state":"SUCCESS"},` +
	`"status_check_rollup":{"id":"SCR_1","state":"PENDING"},"url":"https://github.com/octo-org/example/commit/aaaa1111",` +
	`"links":{"commit_url":"https://github.com/octo-org/example/commit/aaaa1111","resource_path":"/octo-org/example/commit/aaaa1111",` +
	`"commit_resource_path":"/octo-org/example/commit/aaaa1111","tree_url":"https://github.com/octo-org/example/tree/aaaa1111",` +
	`"tree_resource_path":"/octo-org/example/tree/aaaa1111"%s},"tree":{"id":"T_1","oid":"tttt","abbreviated_oid":"ttt",` +
	`"links":{"commit_url":"https://github.com/octo-org/example/commit/aaaa1111","commit_resource_path":"/octo-org/example/commit/aaaa1111"}},` +
	`"viewer_can_subscribe":true,"viewer_subscription":"UNSUBSCRIBED"}`

func fakeBlameWidth(f *fakeGitHub, ranges string) {
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		requests := f.recorded()
		if r.URL.Path != "/api/graphql" || !strings.Contains(requests[len(requests)-1].document, "blame(path") {
			return false
		}
		fmt.Fprintf(w, `{"data":{"repository":{"object":{"__typename":"Commit","blame":{"ranges":[%s]}}}}}`, ranges)
		return true
	}
}

// A blame range carries its age and its commit in full next to the compact members, the author as the flat
// login and as the git actor; the archive addresses of a commit leave only where they carry no token.
func TestBlameCarriesTheFullCommit(t *testing.T) {
	plain := "https://codeload.example.com/octo-org/example/legacy.tar.gz/aaaa1111"
	f := &fakeGitHub{}
	fakeBlameWidth(f, `{"startingLine":1,"endingLine":10,"age":3,"commit":`+widthCommit(plain)+`}`)
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, blameGet.ID, "repo", `{"path":"app.go","end_line":10,"commit_details":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	archives := `,"tarball_url":"` + plain + `","zipball_url":"` + plain + `"`
	assertJSON(t, json.RawMessage(result), `{"ranges":[{"start_line":1,"end_line":10,"sha":"aaaa1111","author":"octocat",`+
		`"date":"2026-03-01T10:00:00+01:00","age":3,"author_detail":`+widthActorView+`,"commit":`+
		strings.Replace(widthCommitView, "%s", archives, 1)+`}],"start_line":1,"end_line":10,"repository":"octo-org/example"}`)

	first, _ := json.Marshal(decodeAny(t, result).(map[string]any)["ranges"].([]any)[0])
	selection := "startingLine endingLine age commit{" + blameCommitSelection + "}"
	if !strings.Contains(blameDetailQuery, selection) {
		t.Fatalf("blameDetailQuery lacks %q", selection)
	}
	if last := f.recorded(); last[len(last)-1].document != blameDetailQuery {
		t.Errorf("commit_details did not send the detail query")
	}
	assertSelectionMatchesOutput(t, selection, first, entrySchema(t, blameGet.OutputSchema, "ranges"),
		[]string{"sha", "author", "date", "author_detail", "links", "type", "message_body_truncated", "start_line",
			"end_line"},
		[]string{"startingLine", "endingLine", "keyFingerprint"})
}

// An archive address with a token, which GitHub says expires, never leaves the tool; a long message body and
// a long signature are cut with their markers, and a commit without the optional members stays compact.
func TestBlameWithholdsTokenAddressesAndCutsLongTexts(t *testing.T) {
	long := strings.Repeat("x", maxCommitMessageLength+1)
	signature := strings.Repeat("s", blameSignatureLimit+1)
	f := &fakeGitHub{}
	fakeBlameWidth(f, `{"startingLine":1,"endingLine":5,"age":1,"commit":{"oid":"cccc",`+
		`"messageBody":"`+long+`","tarballUrl":"https://codeload.example.com/o/r/legacy.tar.gz/cccc?token=SECRET",`+
		`"zipballUrl":"https://codeload.example.com/o/r/legacy.zip/cccc?token=SECRET",`+
		`"signature":{"__typename":"SshSignature","state":"UNKNOWN_KEY","isValid":false,"payload":"`+signature+`",`+
		`"signature":"`+signature+`","keyFingerprint":"SHA256:abc"},"author":null,"onBehalfOf":null,"status":null}},`+
		`{"startingLine":6,"endingLine":9,"commit":{"oid":"dddd"}}`)
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, blameGet.ID, "repo", `{"path":"app.go","end_line":9,"commit_details":true}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "SECRET") || strings.Contains(string(result), "tarball_url") ||
		strings.Contains(string(result), "zipball_url") {
		t.Errorf("a token address left the tool: %s", result)
	}
	var out struct {
		Ranges []struct {
			SHA    string `json:"sha"`
			Commit struct {
				MessageBody          string `json:"message_body"`
				MessageBodyTruncated bool   `json:"message_body_truncated"`
				Signature            struct {
					Type               string `json:"type"`
					Payload            string `json:"payload"`
					PayloadTruncated   bool   `json:"payload_truncated"`
					Signature          string `json:"signature"`
					SignatureTruncated bool   `json:"signature_truncated"`
					KeyFingerprint     string `json:"key_fingerprint"`
				} `json:"signature"`
			} `json:"commit"`
		} `json:"ranges"`
	}
	if err := json.Unmarshal(result, &out); err != nil || len(out.Ranges) != 2 {
		t.Fatalf("result = %s, %v", result, err)
	}
	c := out.Ranges[0].Commit
	if len(c.MessageBody) != maxCommitMessageLength || !c.MessageBodyTruncated ||
		len(c.Signature.Payload) != blameSignatureLimit || !c.Signature.PayloadTruncated ||
		len(c.Signature.Signature) != blameSignatureLimit || !c.Signature.SignatureTruncated ||
		c.Signature.Type != "SshSignature" || c.Signature.KeyFingerprint != "SHA256:abc" {
		t.Errorf("long texts = %+v", c)
	}
	second := decodeAny(t, result).(map[string]any)["ranges"].([]any)[1]
	assertJSON(t, second, `{"start_line":6,"end_line":9,"sha":"dddd","commit":{"oid":"dddd"}}`)
}

// Without commit_details the query asks for the compact commit only and the answer stays compact; a wrong
// type of the argument is refused before a credential is resolved.
func TestBlameCommitDetailsAreOptIn(t *testing.T) {
	compact := `{"oid":"aaaa1111","abbreviatedOid":"aaaa111","messageHeadline":"Fix it",` +
		`"authoredDate":"2026-03-01T10:00:00+01:00","committedDate":"2026-03-01T11:00:00+01:00",` +
		`"author":` + widthActor + `,"committer":` + widthActor + `,` +
		`"url":"https://github.com/octo-org/example/commit/aaaa1111"}`
	f := &fakeGitHub{}
	fakeBlameWidth(f, `{"startingLine":1,"endingLine":10,"age":3,"commit":`+compact+`}`)
	base := serve(t, f)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, &reads), red)

	result, err := invoke(t, core, blameGet.ID, "repo", `{"path":"app.go","end_line":10}`, false)
	if err != nil {
		t.Fatal(err)
	}
	document := f.recorded()[0].document
	if document != blameQuery {
		t.Errorf("default query is not the compact one")
	}
	for _, detail := range []string{"messageBody", "signature", "status", "tree", "tarballUrl", "additions", "message "} {
		if strings.Contains(document, detail) {
			t.Errorf("compact query asks for %q", detail)
		}
	}
	var out struct {
		Ranges []struct {
			Commit map[string]any `json:"commit"`
		} `json:"ranges"`
	}
	if err := json.Unmarshal(result, &out); err != nil || len(out.Ranges) != 1 {
		t.Fatalf("result = %s, %v", result, err)
	}
	for _, key := range []string{"oid", "abbreviated_oid", "message_headline", "authored_date", "committed_date",
		"author", "committer", "url"} {
		if _, ok := out.Ranges[0].Commit[key]; !ok {
			t.Errorf("compact commit lacks %s", key)
		}
	}
	if len(out.Ranges[0].Commit) != 8 {
		t.Errorf("compact commit = %v", out.Ranges[0].Commit)
	}
	first, _ := json.Marshal(decodeAny(t, result).(map[string]any)["ranges"].([]any)[0])
	assertSelectionMatchesOutput(t, "startingLine endingLine age commit{"+blameCompactCommitSelection+"}", first,
		entrySchema(t, blameGet.OutputSchema, "ranges"),
		[]string{"sha", "author", "date", "author_detail", "links", "type", "start_line", "end_line"},
		[]string{"startingLine", "endingLine"})

	before := reads
	if _, err := invoke(t, core, blameGet.ID, "repo", `{"path":"app.go","commit_details":"yes"}`, false); err == nil {
		t.Error("a non-boolean commit_details was accepted")
	}
	if reads != before {
		t.Error("a credential was resolved for a refused argument")
	}
}

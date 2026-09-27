package github

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// blame answers github.blame.get's GraphQL query for the repository bound by the "repo" connection of
// coreConfig: ref "missing-ref" simulates a revision GitHub cannot resolve, path "missing.txt" simulates a
// path with no blame at the ref, and every other request answers with two fixed commit ranges covering
// lines 1-10 and 11-50.
func (f *fakeGitHub) blame(w http.ResponseWriter, document string, variables map[string]any) bool {
	if !strings.Contains(document, "blame(path") {
		return false
	}
	switch {
	case variables["ref"] == "missing-ref":
		w.Write([]byte(`{"data":{"repository":{"object":null}}}`))
	case variables["path"] == "missing.txt":
		w.Write([]byte(`{"data":{"repository":{"object":{"__typename":"Commit","blame":null}}}}`))
	default:
		w.Write([]byte(`{"data":{"repository":{"object":{"__typename":"Commit","blame":{"ranges":[` +
			`{"startingLine":1,"endingLine":10,"commit":{"oid":"aaaa","message":"init",` +
			`"author":{"name":"Ada","date":"2026-01-01T00:00:00Z","user":{"login":"ada"}}}},` +
			`{"startingLine":11,"endingLine":50,"commit":{"oid":"bbbb","message":"more",` +
			`"author":{"name":"Bob","date":"2026-01-05T00:00:00Z","user":null}}}]}}}}}`))
	}
	return true
}

func TestBlameSatisfiesItsContractThroughTheApplicationCore(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	// The default range covers both fixed commit ranges; the login wins over the name, and the name answers
	// where GitHub reports no user.
	result, err := invoke(t, core, blameGet.ID, "repo", `{"path":"app.go"}`, false)
	var blame BlameResult
	want := []BlameRange{
		{StartLine: 1, EndLine: 10, SHA: "aaaa", Author: "ada", Date: "2026-01-01T00:00:00Z"},
		{StartLine: 11, EndLine: 50, SHA: "bbbb", Author: "Bob", Date: "2026-01-05T00:00:00Z"},
	}
	if err != nil || json.Unmarshal(result, &blame) != nil || len(blame.Ranges) != 2 ||
		blame.Ranges[0] != want[0] || blame.Ranges[1] != want[1] {
		t.Fatalf("%s = %s, %v", blameGet.ID, result, err)
	}

	// An explicit range clips GitHub's own ranges to it on both sides.
	result, err = invoke(t, core, blameGet.ID, "repo", `{"path":"app.go","start_line":5,"end_line":15}`, false)
	if err != nil || json.Unmarshal(result, &blame) != nil || len(blame.Ranges) != 2 ||
		blame.Ranges[0].StartLine != 5 || blame.Ranges[0].EndLine != 10 ||
		blame.Ranges[1].StartLine != 11 || blame.Ranges[1].EndLine != 15 {
		t.Fatalf("%s over a clipped range = %s, %v", blameGet.ID, result, err)
	}
	if blame.StartLine != 5 || blame.EndLine != 15 {
		t.Errorf("%s echoed range = %d..%d, want 5..15", blameGet.ID, blame.StartLine, blame.EndLine)
	}

	// A ref GitHub cannot resolve, and a path with no blame at the ref, are both not-found.
	if _, err := invoke(t, core, blameGet.ID, "repo", `{"path":"app.go","ref":"missing-ref"}`, false); classOf(err) != provider.ClassNotFound {
		t.Errorf("%s on a missing ref = %v, want not-found", blameGet.ID, err)
	}
	if _, err := invoke(t, core, blameGet.ID, "repo", `{"path":"missing.txt"}`, false); classOf(err) != provider.ClassNotFound {
		t.Errorf("%s on a missing path = %v, want not-found", blameGet.ID, err)
	}
}

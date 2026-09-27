package github

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// Fixtures the contents and trees fakes answer with for the repository bound by the "repo" connection of
// coreConfig.
const (
	readmeContent = "# Example\n"
	readmeSHA     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	submoduleSHA  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// contents answers the REST Contents API below /repos/octo-org/example/contents for a fixed, small
// repository tree: a text file, a directory, a binary file, a file past GitHub's own inline content limit,
// a text file past Qatlas' own bound, a symlink, and a submodule.
func (f *fakeGitHub) contents(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/v3/repos/octo-org/example/contents"), "/")
	switch path {
	case "":
		fmt.Fprintf(w, `[{"type":"file","name":"README.md","path":"README.md","sha":%q,"size":%d},`+
			`{"type":"dir","name":"internal","path":"internal","sha":"treesha-internal","size":0},`+
			`{"type":"symlink","name":"link","path":"link","sha":"linksha","size":11},`+
			`{"type":"submodule","name":"lib","path":"vendor/lib","sha":%q,"size":0}]`,
			readmeSHA, len(readmeContent), submoduleSHA)
	case "README.md":
		fmt.Fprintf(w, `{"type":"file","path":"README.md","sha":%q,"size":%d,"encoding":"base64","content":%q}`,
			readmeSHA, len(readmeContent), base64.StdEncoding.EncodeToString([]byte(readmeContent)))
	case "internal":
		fmt.Fprint(w, `[{"type":"file","name":"app.go","path":"internal/app.go","sha":"appsha","size":7}]`)
	case "binary.dat":
		data := []byte{0x89, 0x50, 0x4e, 0x00, 0x01}
		fmt.Fprintf(w, `{"type":"file","path":"binary.dat","sha":"binsha","size":%d,"encoding":"base64","content":%q}`,
			len(data), base64.StdEncoding.EncodeToString(data))
	case "huge.bin":
		fmt.Fprint(w, `{"type":"file","path":"huge.bin","sha":"hugesha","size":5000000,"encoding":"none","content":""}`)
	case "big.txt":
		data := []byte(strings.Repeat("a", maxContentsTextBytes+500))
		fmt.Fprintf(w, `{"type":"file","path":"big.txt","sha":"bigsha","size":%d,"encoding":"base64","content":%q}`,
			len(data), base64.StdEncoding.EncodeToString(data))
	case "link":
		fmt.Fprint(w, `{"type":"symlink","path":"link","sha":"linksha","size":11,"target":"README.md"}`)
	case "vendor/lib":
		fmt.Fprintf(w, `{"type":"submodule","path":"vendor/lib","sha":%q,"submodule_git_url":"https://github.com/octo-org/lib"}`,
			submoduleSHA)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}
}

// tree answers the Git Trees API below /repos/octo-org/example/git/trees/ with three fixed refs: "main" for
// a small, non-recursive-aware tree, "cutoff" for one GitHub itself truncated, and "big" for one larger than
// Qatlas' own maxTreeEntries bound.
func (f *fakeGitHub) tree(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimPrefix(r.URL.Path, "/api/v3/repos/octo-org/example/git/trees/")
	recursive := r.URL.Query().Get("recursive") == "1"
	switch ref {
	case "main":
		tree := fmt.Sprintf(`[{"path":"README.md","type":"blob","sha":%q,"size":%d},`+
			`{"path":"internal","type":"tree","sha":"treesha-internal"}]`, readmeSHA, len(readmeContent))
		if recursive {
			tree = fmt.Sprintf(`[{"path":"README.md","type":"blob","sha":%q,"size":%d},`+
				`{"path":"internal/app.go","type":"blob","sha":"appsha","size":7}]`, readmeSHA, len(readmeContent))
		}
		fmt.Fprintf(w, `{"sha":"tree-main","tree":%s,"truncated":false}`, tree)
	case "cutoff":
		fmt.Fprint(w, `{"sha":"tree-cutoff","tree":[{"path":"a","type":"blob","sha":"asha","size":1}],"truncated":true}`)
	case "big":
		entries := make([]string, 0, maxTreeEntries+5)
		for i := 0; i < maxTreeEntries+5; i++ {
			entries = append(entries, fmt.Sprintf(`{"path":"file%d.txt","type":"blob","sha":"sha%d","size":1}`, i, i))
		}
		fmt.Fprintf(w, `{"sha":"tree-big","tree":[%s],"truncated":false}`, strings.Join(entries, ","))
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}
}

func TestContentsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	// A small text file is returned in full.
	result, err := invoke(t, core, contentsGet.ID, "repo", `{"path":"README.md"}`, false)
	if err != nil || !strings.Contains(string(result), `"content":"# Example\n"`) ||
		strings.Contains(string(result), `"truncated"`) || strings.Contains(string(result), `"omitted"`) {
		t.Fatalf("%s on a text file = %s, %v", contentsGet.ID, result, err)
	}

	// The repository root, and a subdirectory, are directory listings.
	result, err = invoke(t, core, contentsGet.ID, "repo", `{}`, false)
	if err != nil || !strings.Contains(string(result), `"type":"dir"`) ||
		!strings.Contains(string(result), `"name":"README.md"`) || !strings.Contains(string(result), `"name":"link"`) {
		t.Fatalf("%s on the root = %s, %v", contentsGet.ID, result, err)
	}
	result, err = invoke(t, core, contentsGet.ID, "repo", `{"path":"internal"}`, false)
	if err != nil || !strings.Contains(string(result), `"path":"internal/app.go"`) {
		t.Fatalf("%s on a subdirectory = %s, %v", contentsGet.ID, result, err)
	}

	// A binary file is metadata only.
	result, err = invoke(t, core, contentsGet.ID, "repo", `{"path":"binary.dat"}`, false)
	if err != nil || !strings.Contains(string(result), `"omitted":"binary"`) || strings.Contains(string(result), `"content"`) {
		t.Fatalf("%s on a binary file = %s, %v", contentsGet.ID, result, err)
	}

	// A file past GitHub's own inline content limit is metadata only.
	result, err = invoke(t, core, contentsGet.ID, "repo", `{"path":"huge.bin"}`, false)
	if err != nil || !strings.Contains(string(result), `"omitted":"too_large"`) || strings.Contains(string(result), `"content"`) {
		t.Fatalf("%s on a too-large file = %s, %v", contentsGet.ID, result, err)
	}

	// A text file past Qatlas' own bound is cut from its start, with the cut visible.
	result, err = invoke(t, core, contentsGet.ID, "repo", `{"path":"big.txt"}`, false)
	if err != nil {
		t.Fatalf("%s on a large text file = %v", contentsGet.ID, err)
	}
	var big struct {
		Size      int  `json:"size"`
		Bytes     int  `json:"bytes"`
		Truncated bool `json:"truncated"`
	}
	if json.Unmarshal(result, &big) != nil || !big.Truncated || big.Bytes != maxContentsTextBytes ||
		big.Size != maxContentsTextBytes+500 {
		t.Fatalf("%s on a large text file = %s (%+v)", contentsGet.ID, result, big)
	}

	// A symlink and a submodule are metadata only, with their target.
	result, err = invoke(t, core, contentsGet.ID, "repo", `{"path":"link"}`, false)
	if err != nil || !strings.Contains(string(result), `"type":"symlink"`) ||
		!strings.Contains(string(result), `"target":"README.md"`) {
		t.Fatalf("%s on a symlink = %s, %v", contentsGet.ID, result, err)
	}
	result, err = invoke(t, core, contentsGet.ID, "repo", `{"path":"vendor/lib"}`, false)
	if err != nil || !strings.Contains(string(result), `"type":"submodule"`) ||
		!strings.Contains(string(result), `"target":"https://github.com/octo-org/lib"`) {
		t.Fatalf("%s on a submodule = %s, %v", contentsGet.ID, result, err)
	}
}

func TestTreesSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	f := &fakeGitHub{}
	base := serve(t, f)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(base), resolver(red, nil), red)

	result, err := invoke(t, core, treesGet.ID, "repo", `{"ref":"main"}`, false)
	if err != nil || !strings.Contains(string(result), `"path":"README.md"`) ||
		strings.Contains(string(result), `"truncated":true`) {
		t.Fatalf("%s = %s, %v", treesGet.ID, result, err)
	}

	result, err = invoke(t, core, treesGet.ID, "repo", `{"ref":"main","recursive":true}`, false)
	if err != nil || !strings.Contains(string(result), `"path":"internal/app.go"`) {
		t.Fatalf("%s recursive = %s, %v", treesGet.ID, result, err)
	}

	// GitHub's own truncation of a ref stays visible.
	result, err = invoke(t, core, treesGet.ID, "repo", `{"ref":"cutoff"}`, false)
	if err != nil || !strings.Contains(string(result), `"truncated":true`) {
		t.Fatalf("%s on a truncated tree = %s, %v", treesGet.ID, result, err)
	}

	// Qatlas' own bound cuts a tree GitHub did not truncate itself.
	result, err = invoke(t, core, treesGet.ID, "repo", `{"ref":"big"}`, false)
	if err != nil {
		t.Fatalf("%s on a large tree = %v", treesGet.ID, err)
	}
	var tree TreeResult
	if json.Unmarshal(result, &tree) != nil || !tree.Truncated || len(tree.Entries) != maxTreeEntries {
		t.Fatalf("%s on a large tree = %d entries, truncated %t; want %d entries, truncated true",
			treesGet.ID, len(tree.Entries), tree.Truncated, maxTreeEntries)
	}
}

// Path, ref, and line-range arguments of the contents, trees, and blame tools are checked before a
// credential is resolved, so a traversal attempt or an unusable ref never reaches GitHub.
func TestContentsPathAndRefAreValidatedBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct {
		name, operation, arguments string
	}{
		{"leading slash", contentsGet.ID, `{"path":"/README.md"}`},
		{"trailing slash", contentsGet.ID, `{"path":"README.md/"}`},
		{"traversal", contentsGet.ID, `{"path":"../secret"}`},
		{"dot segment", contentsGet.ID, `{"path":"a/./b"}`},
		{"control character", contentsGet.ID, "{\"path\":\"a\\u0007b\"}"},
		{"bad ref", contentsGet.ID, `{"path":"README.md","ref":"bad..ref"}`},
		{"traversal in blame", blameGet.ID, `{"path":"../secret"}`},
		{"empty blame path", blameGet.ID, `{"path":""}`},
		{"bad tree ref", treesGet.ID, `{"ref":"bad..ref"}`},
		{"missing tree ref", treesGet.ID, `{}`},
		{"end before start", blameGet.ID, `{"path":"app.go","start_line":10,"end_line":5}`},
		{"span too large", blameGet.ID, fmt.Sprintf(`{"path":"app.go","start_line":1,"end_line":%d}`, maxBlameLines+2)},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.operation, "repo", tt.arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s: %s(%s) = %v, want an invalid request", tt.name, tt.operation, tt.arguments, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s: %s reached the credential or GitHub", tt.name, tt.operation)
		}
	}
}

// A connection whose targets name a project, not a repository, never resolves a credential for a tool that
// binds a repository.
func TestContentsTreesAndBlameRefuseANonRepositoryTargetBeforeIO(t *testing.T) {
	f := &fakeGitHub{}
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(serve(t, f)), resolver(red, &reads), red)

	for _, tt := range []struct{ operation, arguments string }{
		{contentsGet.ID, `{"path":"README.md"}`},
		{treesGet.ID, `{"ref":"main"}`},
		{blameGet.ID, `{"path":"app.go"}`},
	} {
		reads = 0
		before := len(f.recorded())
		if _, err := invoke(t, core, tt.operation, "planning", tt.arguments, false); !isInvalidRequest(err) {
			t.Errorf("%s on a project-scoped connection = %v, want an invalid request", tt.operation, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s on a project-scoped connection reached the credential or GitHub", tt.operation)
		}
	}
}

package github

import (
	"encoding/json"
	"strings"
	"testing"
)

// sameRepositoryJSON compares an answer with want plus the repository member every call on a bound
// repository adds.
func sameRepositoryJSON(t *testing.T, name string, got json.RawMessage, want string) {
	t.Helper()
	var object map[string]any
	if err := json.Unmarshal([]byte(want), &object); err != nil {
		t.Fatalf("%s: want %s: %v", name, want, err)
	}
	object["repository"] = "octo-org/example"
	full, _ := json.Marshal(object)
	sameJSON(t, name, got, string(full))
}

const (
	contentBase = "https://api.github.com/repos/octo-org/example"
	webBase     = "https://github.com/octo-org/example"
	// downloadCanary stands for the temporary token GitHub puts into the download address of a private repository.
	downloadCanary = "download-token-canary-7d1c"
)

func contentLinks(path string) string {
	return `"url":"` + contentBase + `/contents/` + path + `?ref=main","git_url":"` + contentBase + `/git/blobs/3d21ec53",` +
		`"html_url":"` + webBase + `/blob/main/` + path + `","download_url":"https://raw.example.invalid/` + path +
		`?token=` + downloadCanary + `","_links":{"self":"` + contentBase + `/contents/` + path + `?ref=main",` +
		`"git":"` + contentBase + `/git/blobs/3d21ec53","html":"` + webBase + `/blob/main/` + path + `"}`
}

// The content documented by GitHub's REST reference for "Get repository content": a file, with the
// download address a private repository answers with a token.
var documentedContentFile = `{"type":"file","encoding":"base64","size":5,"name":"README.md","path":"README.md",` +
	`"content":"aGVsbG8=\n","sha":"3d21ec53",` + contentLinks("README.md") + `}`

var documentedContentDirectory = `[{"type":"file","size":625,"name":"octokit.rb","path":"lib/octokit.rb",` +
	`"sha":"fff6fe3a",` + contentLinks("lib/octokit.rb") + `},{"type":"dir","size":0,"name":"octokit",` +
	`"path":"lib/octokit","sha":"a84d88e7",` + strings.Replace(contentLinks("lib/octokit"), `"download_url":"https://raw.example.invalid/lib/octokit?token=`+downloadCanary+`"`, `"download_url":null`, 1) + `}]`

func TestContentsGetReportsEveryDocumentedMember(t *testing.T) {
	_, core := cannedCore(t, documentedContentFile, false, discoveryConfig)
	got, err := invoke(t, core, contentsGet.ID, "repo", `{"path":"README.md","ref":"main"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), downloadCanary) || strings.Contains(string(got), "download_url") ||
		strings.Contains(string(got), "_links") {
		t.Errorf("the answer holds a withheld value: %s", got)
	}
	sameRepositoryJSON(t, "file", got, `{"path":"README.md","type":"file","size":5,"sha":"3d21ec53","content":"hello","bytes":5,`+
		`"name":"README.md","encoding":"base64","url":"`+webBase+`/blob/main/README.md",`+
		`"links":{"url":"`+contentBase+`/contents/README.md?ref=main","git_url":"`+contentBase+`/git/blobs/3d21ec53"}}`)
}

func TestContentsGetReportsSymlinkAndSubmoduleAddresses(t *testing.T) {
	const symlink = `{"type":"symlink","target":"../lib","size":6,"name":"link","path":"link","sha":"aa11",` +
		`"url":"` + contentBase + `/contents/link","git_url":"` + contentBase + `/git/blobs/aa11",` +
		`"html_url":"` + webBase + `/blob/main/link","download_url":null}`
	_, core := cannedCore(t, symlink, false, discoveryConfig)
	got, err := invoke(t, core, contentsGet.ID, "repo", `{"path":"link"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameRepositoryJSON(t, "symlink", got, `{"path":"link","type":"symlink","size":6,"sha":"aa11","target":"../lib","name":"link",`+
		`"url":"`+webBase+`/blob/main/link","links":{"url":"`+contentBase+`/contents/link",`+
		`"git_url":"`+contentBase+`/git/blobs/aa11"}}`)

	const submodule = `{"type":"submodule","submodule_git_url":"git://github.com/jquery/qunit.git","size":0,` +
		`"name":"qunit","path":"qunit","sha":"bb22","url":"` + contentBase + `/contents/qunit",` +
		`"git_url":"` + contentBase + `/git/trees/bb22","html_url":"https://github.com/jquery/qunit/tree/bb22",` +
		`"download_url":null}`
	_, core = cannedCore(t, submodule, false, discoveryConfig)
	got, err = invoke(t, core, contentsGet.ID, "repo", `{"path":"qunit"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameRepositoryJSON(t, "submodule", got, `{"path":"qunit","type":"submodule","sha":"bb22","target":"git://github.com/jquery/qunit.git",`+
		`"name":"qunit","url":"https://github.com/jquery/qunit/tree/bb22","links":{"url":"`+contentBase+`/contents/qunit",`+
		`"git_url":"`+contentBase+`/git/trees/bb22","submodule_git_url":"git://github.com/jquery/qunit.git"}}`)
}

func TestContentsGetDirectoryReportsEntryAddresses(t *testing.T) {
	_, core := cannedCore(t, documentedContentDirectory, false, discoveryConfig)
	got, err := invoke(t, core, contentsGet.ID, "repo", `{"path":"lib"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), downloadCanary) || strings.Contains(string(got), "download_url") ||
		strings.Contains(string(got), "_links") {
		t.Errorf("the answer holds a withheld value: %s", got)
	}
	sameRepositoryJSON(t, "directory", got, `{"path":"lib","type":"dir","entries":[`+
		`{"name":"octokit.rb","path":"lib/octokit.rb","type":"file","size":625,"sha":"fff6fe3a",`+
		`"url":"`+webBase+`/blob/main/lib/octokit.rb","links":{"url":"`+contentBase+`/contents/lib/octokit.rb?ref=main",`+
		`"git_url":"`+contentBase+`/git/blobs/3d21ec53"}},`+
		`{"name":"octokit","path":"lib/octokit","type":"dir","sha":"a84d88e7",`+
		`"url":"`+webBase+`/blob/main/lib/octokit","links":{"url":"`+contentBase+`/contents/lib/octokit?ref=main",`+
		`"git_url":"`+contentBase+`/git/blobs/3d21ec53"}}]}`)
}

func TestContentsWithoutNewMembersKeepTheirOldAnswer(t *testing.T) {
	_, core := cannedCore(t, `{"type":"file","encoding":"base64","size":5,"path":"a.txt","content":"aGVsbG8=","sha":"s1"}`,
		false, discoveryConfig)
	got, err := invoke(t, core, contentsGet.ID, "repo", `{"path":"a.txt"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameRepositoryJSON(t, "file", got, `{"path":"a.txt","type":"file","size":5,"sha":"s1","content":"hello","bytes":5,"encoding":"base64"}`)

	_, core = cannedCore(t, `[{"type":"file","name":"a","path":"d/a","sha":"s1","size":1}]`, false, discoveryConfig)
	got, err = invoke(t, core, contentsGet.ID, "repo", `{"path":"d"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameRepositoryJSON(t, "dir", got, `{"path":"d","type":"dir","entries":[{"name":"a","path":"d/a","type":"file","size":1,"sha":"s1"}]}`)
}

func TestTreesGetReportsEveryDocumentedMember(t *testing.T) {
	const body = `{"sha":"9fb037999f26","url":"` + contentBase + `/git/trees/9fb037999f26","tree":[` +
		`{"path":"file.rb","mode":"100644","type":"blob","size":132,"sha":"44b4fc6d",` +
		`"url":"` + contentBase + `/git/blobs/44b4fc6d"},` +
		`{"path":"subdir","mode":"040000","type":"tree","sha":"f484d249","url":"` + contentBase + `/git/trees/f484d249"}],` +
		`"truncated":false}`
	_, core := cannedCore(t, body, false, discoveryConfig)
	got, err := invoke(t, core, treesGet.ID, "repo", `{"ref":"main"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameRepositoryJSON(t, "tree", got, `{"sha":"9fb037999f26","ref":"main","recursive":false,"truncated":false,`+
		`"links":{"url":"`+contentBase+`/git/trees/9fb037999f26"},"entries":[`+
		`{"path":"file.rb","type":"blob","sha":"44b4fc6d","size":132,"mode":"100644","links":{"url":"`+contentBase+`/git/blobs/44b4fc6d"}},`+
		`{"path":"subdir","type":"tree","sha":"f484d249","mode":"040000","links":{"url":"`+contentBase+`/git/trees/f484d249"}}]}`)

	_, core = cannedCore(t, `{"sha":"s","tree":[{"path":"a","type":"blob","sha":"b"}]}`, false, discoveryConfig)
	got, err = invoke(t, core, treesGet.ID, "repo", `{"ref":"main"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameRepositoryJSON(t, "old", got, `{"sha":"s","ref":"main","recursive":false,"truncated":false,`+
		`"entries":[{"path":"a","type":"blob","sha":"b"}]}`)
}

const workflowEntryExample = `{"type":"file","size":3,"name":"ci.yml","path":".github/workflows/ci.yml",` +
	`"sha":"0123456789abcdef0123456789abcdef01234567","url":"` + contentBase + `/contents/.github/workflows/ci.yml?ref=main",` +
	`"git_url":"` + contentBase + `/git/blobs/0123456789abcdef0123456789abcdef01234567",` +
	`"html_url":"` + webBase + `/blob/main/.github/workflows/ci.yml",` +
	`"download_url":"https://raw.example.invalid/ci.yml?token=` + downloadCanary + `",` +
	`"_links":{"self":"` + contentBase + `/contents/.github/workflows/ci.yml?ref=main"}}`

func TestWorkflowFilesReportEveryDocumentedMember(t *testing.T) {
	_, core := cannedCore(t, `[`+workflowEntryExample+`]`, false, maintainConfig)
	got, err := invoke(t, core, workflowFilesList.ID, "maintainer", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), downloadCanary) || strings.Contains(string(got), "download_url") {
		t.Errorf("the list holds a withheld value: %s", got)
	}
	sameRepositoryJSON(t, "list", got, `{"files":[{"name":"ci.yml","path":".github/workflows/ci.yml",`+
		`"sha":"0123456789abcdef0123456789abcdef01234567","size":3,"type":"file",`+
		`"url":"`+webBase+`/blob/main/.github/workflows/ci.yml",`+
		`"links":{"url":"`+contentBase+`/contents/.github/workflows/ci.yml?ref=main",`+
		`"git_url":"`+contentBase+`/git/blobs/0123456789abcdef0123456789abcdef01234567"}}]}`)

	file := strings.Replace(workflowEntryExample, `"size":3,`, `"size":3,"encoding":"base64","content":"b246Cg==",`, 1)
	file = strings.Replace(file, `"size":3,"encoding"`, `"size":4,"encoding"`, 1)
	_, core = cannedCore(t, file, false, maintainConfig)
	got, err = invoke(t, core, workflowFilesGet.ID, "maintainer", `{"path":".github/workflows/ci.yml"}`, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), downloadCanary) || strings.Contains(string(got), "download_url") {
		t.Errorf("the file holds a withheld value: %s", got)
	}
	sameRepositoryJSON(t, "get", got, `{"path":".github/workflows/ci.yml","sha":"0123456789abcdef0123456789abcdef01234567",`+
		`"size":4,"content":"on:\n","name":"ci.yml","type":"file","encoding":"base64",`+
		`"url":"`+webBase+`/blob/main/.github/workflows/ci.yml",`+
		`"links":{"url":"`+contentBase+`/contents/.github/workflows/ci.yml?ref=main",`+
		`"git_url":"`+contentBase+`/git/blobs/0123456789abcdef0123456789abcdef01234567"}}`)
}

func TestWorkflowFilesWithoutNewMembersKeepTheirOldAnswer(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	_, core := cannedCore(t, `[{"type":"file","name":"ci.yml","path":".github/workflows/ci.yml","sha":"`+sha+`","size":3}]`,
		false, maintainConfig)
	got, err := invoke(t, core, workflowFilesList.ID, "maintainer", `{}`, false)
	if err != nil {
		t.Fatal(err)
	}
	sameRepositoryJSON(t, "list", got, `{"files":[{"name":"ci.yml","path":".github/workflows/ci.yml","sha":"`+sha+`","size":3,"type":"file"}]}`)
}

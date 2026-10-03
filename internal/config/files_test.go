package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

// filesProviders is testProviders plus a provider with one tool per direction of local file access, and a
// provider that only reads local files.
type filesProviders struct{ ProviderCatalog }

func (p filesProviders) ProviderMetadata(id string) (ProviderMetadata, bool) {
	switch id {
	case "filer":
		return ProviderMetadata{ID: "filer", Name: "Filer", LocalFiles: LocalFilesSupport{Read: true, Write: true},
			SecretRoles: []SecretRole{{Name: "token", Description: "token"}}, Target: TargetMetadata{Label: "target"}}, true
	case "uploader":
		return ProviderMetadata{ID: "uploader", Name: "Uploader", LocalFiles: LocalFilesSupport{Read: true},
			SecretRoles: []SecretRole{{Name: "token", Description: "token"}}, Target: TargetMetadata{Label: "target"}}, true
	}
	return p.ProviderCatalog.ProviderMetadata(id)
}

const filesBase = `
version: 1
services:
  docs:
    provider: filer
    base_url: https://docs.example.invalid
  up:
    provider: uploader
    base_url: https://up.example.invalid
  wiki:
    provider: bookstack
    base_url: https://wiki.example.invalid
credentials:
  key:
    type: env
    values:
      token: DOCS_TOKEN
  wikikey:
    type: env
    values:
      token-id: WIKI_TOKEN_ID
      token-secret: WIKI_TOKEN_SECRET
connections:
  docs:
    service: docs
    credential: key
`

func decodeFiles(t *testing.T, doc string) (*Config, error) {
	t.Helper()
	return Decode(strings.NewReader(doc), filesProviders{testProviders})
}

func TestFilesAreValidated(t *testing.T) {
	dir := t.TempDir()
	cfg, err := decodeFiles(t, filesBase+"    files:\n      read: ["+dir+", \"~/in\"]\n      write: ["+dir+"]\n")
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	want := Files{Read: []string{dir, "~/in"}, Write: []string{dir}}
	if got := cfg.Connections["docs"].Files; !reflect.DeepEqual(got, want) {
		t.Errorf("files = %+v, want %+v", got, want)
	}

	const canary = "files-canary-58d2"
	for _, tt := range []struct {
		name, files, want string
	}{
		{"relative", "read: [" + canary + "/in]", "files.read[0]: must name a directory"},
		{"glob", "write: [\"/srv/" + canary + "-*\"]", "files.write[0]: glob patterns are not supported"},
		{"root", "read: [" + drive + "/]", "files.read[0]: must name a directory below the file system root"},
		{"bare home", "write: [\"~\"]", "files.write[0]: must name a directory below the file system root"},
		{"home with slash", "write: [\"~/\"]", "files.write[0]: must name a directory below"},
		{"parent segment", "read: [\"" + drive + "/srv/" + canary + "/../x\"]", "files.read[0]: must not contain '..'"},
		{"duplicate by clean", "read: [" + drive + "/srv/" + canary + ", " + drive + "/srv/" + canary + "/]",
			"files.read[1]: a directory is listed more than once"},
		{"empty list", "read: []", "files.read: must name at least one directory"},
		{"empty entry", "write: [\"\"]", "files.write[0]: must not be empty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeFiles(t, filesBase+"    files: {"+tt.files+"}\n")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Decode() = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), canary) {
				t.Errorf("error = %q quotes the entry", err)
			}
		})
	}

	// The same directory in both directions is no duplicate.
	if _, err := decodeFiles(t, filesBase+"    files: {read: ["+drive+"/srv/a], write: ["+drive+"/srv/a]}\n"); err != nil {
		t.Errorf("same directory in both directions: %v", err)
	}
}

func TestFilesNeedAProviderWithToolsOfTheDirection(t *testing.T) {
	for _, tt := range []struct {
		name, service, files, want string
	}{
		{"no tools at all, read", "wiki", "read: [" + drive + "/srv/a]", "files.read: the provider of this connection has no tool that reads local files"},
		{"no tools at all, write", "wiki", "write: [" + drive + "/srv/a]", "files.write: the provider of this connection has no tool that writes local files"},
		{"only uploads, write refused", "up", "write: [" + drive + "/srv/a]", "files.write: the provider of this connection has no tool that writes local files"},
		{"only uploads, read fine", "up", "read: [" + drive + "/srv/a]", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			credential := map[string]string{"wiki": "wikikey"}[tt.service]
			if credential == "" {
				credential = "key"
			}
			doc := strings.Replace(filesBase, "service: docs\n    credential: key",
				"service: "+tt.service+"\n    credential: "+credential, 1) + "    files: {" + tt.files + "}\n"
			_, err := decodeFiles(t, doc)
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("Decode() = %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("Decode() = %v, want %q", err, tt.want)
			}
		})
	}
	// Without the field nothing is refused.
	if _, err := decodeFiles(t, filesBase); err != nil {
		t.Errorf("Decode() without files = %v", err)
	}
}

func TestFilesWarningsNameMissingDirectoriesByPosition(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")
	cfg, err := decodeFiles(t, filesBase+"    files:\n      read: ["+dir+", "+missing+"]\n      write: ["+file+"]\n")
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	want := []string{
		`connection "docs": files.read[1] names no existing directory, so no file can be read from there`,
		`connection "docs": files.write[0] names no existing directory, so no file can be written to there`,
	}
	if got := cfg.FilesWarnings(); !reflect.DeepEqual(got, want) {
		t.Errorf("FilesWarnings() = %q, want %q", got, want)
	}
}

func TestFilesSurviveMarshalCloneAndResolve(t *testing.T) {
	cfg, err := decodeFiles(t, filesBase+"    files: {read: ["+drive+"/srv/in], write: [\"~/out\"]}\n")
	if err != nil {
		t.Fatal(err)
	}
	want := Files{Read: []string{drive + "/srv/in"}, Write: []string{"~/out"}}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	again, err := decodeFiles(t, string(data))
	if err != nil || !reflect.DeepEqual(again.Connections["docs"].Files, want) {
		t.Fatalf("round trip = %+v, %v\n%s", again.Connections["docs"].Files, err, data)
	}
	clone := cfg.Clone()
	clone.Connections["docs"].Files.Read[0] = "/elsewhere"
	if got := cfg.Connections["docs"].Files; !reflect.DeepEqual(got, want) {
		t.Errorf("files after editing a clone = %+v", got)
	}
	resolved, err := cfg.Resolve("docs", "")
	if err != nil || !reflect.DeepEqual(resolved.Files, want) {
		t.Errorf("Resolve() files = %+v, %v", resolved, err)
	}
	plain, err := decodeFiles(t, filesBase)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := yaml.Marshal(plain); strings.Contains(string(data), "files") {
		t.Errorf("a connection without files is saved with them:\n%s", data)
	}
}

func TestConnectionRefusalNeedsFilesOfTheToolsDirection(t *testing.T) {
	cfg, err := decodeFiles(t, filesBase+"    permissions: [read, create]\n    files: {read: ["+drive+"/srv/in]}\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		direction LocalFiles
		want      Refusal
	}{
		{"", ""}, {LocalFilesRead, ""}, {LocalFilesWrite, RefusalNoLocalFiles},
	} {
		tool := ToolMetadata{ID: "filer.files.x", Effect: PermissionRead, LocalFiles: tt.direction}
		if got := cfg.ConnectionRefusal("docs", tool); got != tt.want {
			t.Errorf("ConnectionRefusal(%q) = %q, want %q", tt.direction, got, tt.want)
		}
	}
}

package nextcloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var unusableETags = map[string]string{
	"a star": "*", "a quoted star": `"*"`, "empty": "", "blank": "   ",
	"a control character": "v1\nx", "too long": strings.Repeat("a", maxValueLength+1),
}

// update only replaces: an unusable etag is refused before any secret access, file access, or request.
func TestUpdateRefusesUnusableETagsBeforeSecretsAndRequests(t *testing.T) {
	refuse(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, etag := range unusableETags {
		for _, content := range []string{
			`"content_base64":"aGVsbG8="`,
			`"local_path":` + strconv.Quote(source),
		} {
			t.Run(name+" "+content[:8], func(t *testing.T) {
				reads := 0
				red := &redact.Redactor{}
				secrets := secret.NewWith(func(string) string { reads++; return "x" }, nil, nil, red)
				args := `{"path":"new.txt","etag":` + strconv.Quote(etag) + `,` + content + `}`
				_, err := invokeFilesUpdate(context.Background(), localConnection(dir, ""), secrets, red, json.RawMessage(args))
				if classOf(err) != provider.ClassProviderError {
					t.Fatalf("class = %q, error %v", classOf(err), err)
				}
				if reads != 0 {
					t.Errorf("secret reads = %d, want 0", reads)
				}
			})
		}
	}
}

// The schema refuses the same forms before the handler runs.
func TestUpdateSchemaRefusesStarETags(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)
	for _, etag := range []string{"*", `"*"`, "", " "} {
		_, err := core.Invoke(context.Background(), application.InvokeRequest{
			Operation: "nextcloud.files.update", Connection: "reports", Confirmed: true,
			Arguments: json.RawMessage(`{"path":"new.txt","content_base64":"aGk=","etag":` + strconv.Quote(etag) + `}`)})
		if err == nil {
			t.Errorf("etag %q was accepted", etag)
		}
	}
}

// A connection that may read and update cannot create a file: create is not offered and update with * is refused.
func TestUpdateOnlyConnectionCannotCreateAFile(t *testing.T) {
	refuse(t)
	cfg := coreConfig()
	conn := cfg.Connections["reports"]
	conn.Permissions = []config.Permission{config.PermissionRead, config.PermissionUpdate}
	cfg.Connections["reports"] = conn
	red := &redact.Redactor{}
	core := application.New(registry(t), cfg, resolver(red), red)
	for _, request := range []application.InvokeRequest{
		{Operation: "nextcloud.files.create", Connection: "reports", Confirmed: true,
			Arguments: json.RawMessage(`{"path":"brandnew.txt","content_base64":"aGk="}`)},
		{Operation: "nextcloud.files.update", Connection: "reports", Confirmed: true,
			Arguments: json.RawMessage(`{"path":"brandnew.txt","content_base64":"aGk=","etag":"*"}`)},
	} {
		if _, err := core.Invoke(context.Background(), request); err == nil {
			t.Errorf("%s was accepted", request.Operation)
		}
	}
}

func TestWriteConditionFollowsTheTool(t *testing.T) {
	var puts []string
	serve(t, func(request *http.Request) (*http.Response, error) {
		puts = append(puts, request.Header.Get("If-Match")+"|"+request.Header.Get("If-None-Match"))
		return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	red := &redact.Redactor{}
	args := func(etag string) json.RawMessage {
		return json.RawMessage(`{"path":"note.txt","content_base64":"aGk="` + etag + `}`)
	}
	if _, err := invokeFilesCreate(context.Background(), localConnection("", ""), resolver(red), red, args("")); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeFilesUpdate(context.Background(), localConnection("", ""), resolver(red), red, args(`,"etag":"v1"`)); err != nil {
		t.Fatal(err)
	}
	if len(puts) != 2 || puts[0] != "|*" || puts[1] != `"v1"|` {
		t.Errorf("conditions = %v", puts)
	}
}

package nextcloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	versionFolder = "/remote.php/dav/versions/" + aliceUser + "/versions/1003"
	versionID     = "1760000000"
)

func versionXML(href, length, modified string) string {
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop>` +
		`<d:resourcetype/><d:getcontenttype>text/plain</d:getcontenttype>` +
		`<d:getcontentlength>` + length + `</d:getcontentlength>` +
		`<d:getlastmodified>` + modified + `</d:getlastmodified>` +
		`<d:getetag>&quot;v-` + length + `&quot;</d:getetag>` +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

// versionServer answers the stat of note.txt, the version list, and the version content; MOVEs are
// recorded and answered with moveAnswer.
func versionServer(t *testing.T, list string, moveAnswer func() (*http.Response, error)) (*[]call, *[]*http.Request) {
	t.Helper()
	var moves []*http.Request
	calls := serve(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == methodPropfind && strings.HasPrefix(request.URL.Path, "/remote.php/dav/files/"):
			if strings.HasSuffix(request.URL.Path, "/folder") {
				return xmlResponse(http.StatusMultiStatus, multistatus(folderXML(aliceRoot+"/folder/", "folder", "1010", "0"))), nil
			}
			return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
		case request.Method == methodPropfind && request.URL.Path == versionFolder:
			return xmlResponse(http.StatusMultiStatus, list), nil
		case request.Method == http.MethodGet && request.URL.Path == versionFolder+"/"+versionID:
			return &http.Response{StatusCode: 200, ContentLength: 5, Header: http.Header{"Content-Type": {"text/plain"}}, Body: io.NopCloser(strings.NewReader("hello"))}, nil
		case request.Method == methodMove:
			moves = append(moves, request)
			return moveAnswer()
		}
		t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		return status(500), nil
	})
	return calls, &moves
}

func okMove() (*http.Response, error) { return status(201), nil }

func ownList() string {
	return multistatus(
		folderXML(versionFolder+"/", "1003", "1003", "0"),
		versionXML(versionFolder+"/1750000000", "4", "Mon, 02 Mar 2026 11:15:00 GMT"),
		versionXML(versionFolder+"/"+versionID, "5", "Wed, 01 Apr 2026 07:05:00 GMT"),
		versionXML(versionFolder+"/notatimestamp", "5", "Wed, 01 Apr 2026 07:05:00 GMT"),
	)
}

func TestVersionsListIsNewestFirstAndFixed(t *testing.T) {
	calls, _ := versionServer(t, ownList(), okMove)
	red := &redact.Redactor{}
	got, err := invokeVersionsList(context.Background(), localConnection("", ""), resolver(red), red, json.RawMessage(`{"path":"note.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*VersionList)
	if list.Count != 2 || list.Truncated || list.Versions[0].VersionID != versionID || list.Versions[1].VersionID != "1750000000" ||
		list.Versions[0].Size != 5 || list.Versions[0].ETag != "v-5" || list.Versions[0].ModifiedAt != "2026-04-01T07:05:00Z" {
		t.Errorf("list = %+v", list)
	}
	if len(*calls) != 2 || (*calls)[1].url.Path != versionFolder || (*calls)[1].depth != depthChildren || (*calls)[1].method != methodPropfind {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestVersionsListIsCappedAndReportsTheCut(t *testing.T) {
	entries := []string{folderXML(versionFolder+"/", "1003", "1003", "0")}
	for i := 0; i < maxVersions+5; i++ {
		entries = append(entries, versionXML(fmt.Sprintf("%s/%d", versionFolder, 1700000000+i), "1", "Wed, 01 Apr 2026 07:05:00 GMT"))
	}
	versionServer(t, multistatus(entries...), okMove)
	red := &redact.Redactor{}
	got, err := invokeVersionsList(context.Background(), localConnection("", ""), resolver(red), red, json.RawMessage(`{"path":"note.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*VersionList)
	if list.Count != maxVersions || !list.Truncated || list.Versions[0].VersionID != strconv.Itoa(1700000000+maxVersions+4) {
		t.Errorf("count = %d, truncated = %v, first = %s", list.Count, list.Truncated, list.Versions[0].VersionID)
	}
}

func TestVersionsListRefusesForeignNodes(t *testing.T) {
	for name, href := range map[string]string{
		"other file":    "/remote.php/dav/versions/" + aliceUser + "/versions/1004/1750000000",
		"other user":    "/remote.php/dav/versions/" + bobUser + "/versions/1003/1750000000",
		"files area":    "/remote.php/dav/files/" + aliceUser + "/Reports/1750000000",
		"deeper":        versionFolder + "/1750000000/x",
		"other origin":  "https://evil.example.invalid" + versionFolder + "/1750000000",
		"traversal":     versionFolder + "/../1004/1750000000",
		"encoded slash": versionFolder + "/1750000000%2F..%2F1004",
	} {
		t.Run(name, func(t *testing.T) {
			versionServer(t, multistatus(folderXML(versionFolder+"/", "1003", "1003", "0"),
				versionXML(href, "1", "Wed, 01 Apr 2026 07:05:00 GMT")), okMove)
			red := &redact.Redactor{}
			if _, err := invokeVersionsList(context.Background(), localConnection("", ""), resolver(red), red, json.RawMessage(`{"path":"note.txt"}`)); err == nil {
				t.Error("a foreign node was accepted")
			}
		})
	}
}

func TestVersionsRefuseFoldersAndBadInputBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	reads := 0
	red := &redact.Redactor{}
	secrets := secret.NewWith(func(string) string { reads++; return "x" }, nil, nil, red)
	dir := t.TempDir()
	conn := localConnection(dir, dir)
	out := strconv.Quote(filepath.Join(dir, "out.txt"))
	type invoker func(context.Context, json.RawMessage) (any, error)
	call := func(fn func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error)) invoker {
		return func(ctx context.Context, raw json.RawMessage) (any, error) { return fn(ctx, conn, secrets, red, raw) }
	}
	tools := map[string]invoker{"list": call(invokeVersionsList), "get": call(invokeVersionsGet), "restore": call(invokeVersionsRestore)}
	cases := map[string]string{
		"traversal":         `{"path":"../x","version_id":"1","etag":"e"}`,
		"absolute":          `{"path":"/x","version_id":"1","etag":"e"}`,
		"percent":           `{"path":"a%2Fb","version_id":"1","etag":"e"}`,
		"empty path":        `{"path":"","version_id":"1","etag":"e"}`,
		"letters":           `{"path":"a","version_id":"abc","etag":"e"}`,
		"separator":         `{"path":"a","version_id":"1/2","etag":"e"}`,
		"dots":              `{"path":"a","version_id":"..","etag":"e"}`,
		"empty version":     `{"path":"a","version_id":"","etag":"e"}`,
		"too long":          `{"path":"a","version_id":"123456789012345678901","etag":"e"}`,
		"file_id":           `{"path":"a","version_id":"1","etag":"e","file_id":"1004"}`,
		"unknown":           `{"path":"a","version_id":"1","etag":"e","depth":"infinity"}`,
		"local with get id": `{"path":"a","version_id":"x","local_path":` + out + `}`,
	}
	for toolName, tool := range tools {
		for name, args := range cases {
			if toolName == "list" && (name == "letters" || name == "separator" || name == "dots" || name == "empty version" || name == "too long" || name == "local with get id") {
				continue
			}
			if _, err := tool(capability.WithConfirmed(context.Background()), json.RawMessage(args)); err == nil {
				t.Errorf("%s %s was accepted", toolName, name)
			}
		}
	}
	for _, etag := range []string{"*", `"*"`, "", "  "} {
		if _, err := invokeVersionsRestore(capability.WithConfirmed(context.Background()), conn, secrets, red,
			json.RawMessage(`{"path":"a","version_id":"1","etag":`+strconv.Quote(etag)+`}`)); err == nil {
			t.Errorf("etag %q was accepted", etag)
		}
	}
	if reads != 0 {
		t.Errorf("secret reads = %d, want 0", reads)
	}
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a local file exists: %v", err)
	}
}

// The schema refuses the same forms, so neither CLI nor MCP reaches the handler with them.
func TestVersionsSchemaRefusesFileIDsAndBadVersionIDs(t *testing.T) {
	refuse(t)
	red := &redact.Redactor{}
	core := application.New(registry(t), coreConfig(), resolver(red), red)
	for op, args := range map[string][]string{
		"nextcloud.versions.list": {`{"path":"a","file_id":"1004"}`, `{"path":"../a"}`},
		"nextcloud.versions.get": {`{"path":"a","version_id":"1","file_id":"1004"}`, `{"path":"a","version_id":"1a"}`,
			`{"path":"a","version_id":"123456789012345678901"}`, `{"path":"a"}`},
		"nextcloud.versions.restore": {`{"path":"a","version_id":"1","etag":"e","file_id":"1004"}`, `{"path":"a","version_id":"x","etag":"e"}`,
			`{"path":"a","version_id":"1"}`, `{"path":"a","version_id":"1","etag":"*"}`},
	} {
		for _, a := range args {
			if _, err := core.Invoke(context.Background(), application.InvokeRequest{Operation: op, Connection: "reports",
				Confirmed: true, Arguments: json.RawMessage(a)}); err == nil {
				t.Errorf("%s accepted %s", op, a)
			}
		}
	}
}

func TestVersionsRefuseAFolder(t *testing.T) {
	versionServer(t, ownList(), okMove)
	red := &redact.Redactor{}
	for name, fn := range map[string]func() error{
		"list": func() error {
			_, err := invokeVersionsList(context.Background(), localConnection("", ""), resolver(red), red, json.RawMessage(`{"path":"folder"}`))
			return err
		},
		"get": func() error {
			_, err := invokeVersionsGet(context.Background(), localConnection("", ""), resolver(red), red, json.RawMessage(`{"path":"folder","version_id":"1"}`))
			return err
		},
		"restore": func() error {
			_, err := invokeVersionsRestore(capability.WithConfirmed(context.Background()), localConnection("", ""), resolver(red), red,
				json.RawMessage(`{"path":"folder","version_id":"1","etag":"60f1c8a2e4b19"}`))
			return err
		},
	} {
		if err := fn(); err == nil {
			t.Errorf("%s accepted a folder", name)
		}
	}
}

func TestVersionsGetInlineAndLimit(t *testing.T) {
	calls, _ := versionServer(t, ownList(), okMove)
	red := &redact.Redactor{}
	args := json.RawMessage(`{"path":"note.txt","version_id":"` + versionID + `"}`)
	got, err := invokeVersionsGet(context.Background(), localConnection("", ""), resolver(red), red, args)
	if err != nil {
		t.Fatal(err)
	}
	content := got.(*VersionContent)
	if content.Size != 5 || content.VersionID != versionID || content.ContentBase64 != base64.StdEncoding.EncodeToString([]byte("hello")) {
		t.Errorf("content = %+v", content)
	}
	if len(*calls) != 2 || (*calls)[1].method != http.MethodGet || (*calls)[1].url.Path != versionFolder+"/"+versionID {
		t.Errorf("calls = %+v", *calls)
	}

	serve(t, func(request *http.Request) (*http.Response, error) {
		if request.Method == methodPropfind {
			return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(strings.Repeat("a", maxFileBytes+1)))}, nil
	})
	if _, err := invokeVersionsGet(context.Background(), localConnection("", ""), resolver(red), red, args); err == nil ||
		!strings.Contains(err.Error(), "4 MiB") {
		t.Errorf("err = %v, want the inline limit", err)
	}
}

func TestVersionsGetToAReleasedFolder(t *testing.T) {
	dir := t.TempDir()
	versionServer(t, ownList(), okMove)
	red := &redact.Redactor{}
	target := filepath.Join(dir, "old.txt")
	args := json.RawMessage(`{"path":"note.txt","version_id":"` + versionID + `","local_path":` + strconv.Quote(target) + `}`)
	got, err := invokeVersionsGet(context.Background(), localConnection("", dir), resolver(red), red, args)
	if err != nil {
		t.Fatal(err)
	}
	result := got.(*VersionDownload)
	if result.Size != 5 || result.SHA256 != helloSHA || result.Name != "note.txt" || result.VersionID != versionID {
		t.Errorf("result = %+v", result)
	}
	if raw, _ := os.ReadFile(target); string(raw) != "hello" {
		t.Errorf("file = %q", raw)
	}
	if strings.Contains(fmt.Sprint(result), "aGVsbG8") {
		t.Error("content in the result")
	}

	// A folder that is not released refuses before any request.
	refuse(t)
	other := filepath.Join(t.TempDir(), "x.txt")
	var pathErr *localfile.PathError
	_, err = invokeVersionsGet(context.Background(), localConnection("", dir), resolver(red), red,
		json.RawMessage(`{"path":"note.txt","version_id":"`+versionID+`","local_path":`+strconv.Quote(other)+`}`))
	if !errors.As(err, &pathErr) {
		t.Errorf("err = %v", err)
	}
}

func TestVersionsRestoreChecksTheETagThenMovesOnce(t *testing.T) {
	calls, moves := versionServer(t, ownList(), okMove)
	red := &redact.Redactor{}
	run := func(etag string) (any, error) {
		return invokeVersionsRestore(capability.WithConfirmed(context.Background()), localConnection("", ""), resolver(red), red,
			json.RawMessage(`{"path":"note.txt","version_id":"`+versionID+`","etag":`+strconv.Quote(etag)+`}`))
	}
	if _, err := run("stale"); err == nil || !strings.Contains(err.Error(), "changed") || len(*moves) != 0 {
		t.Fatalf("err = %v, moves = %d", err, len(*moves))
	}
	got, err := run(`"` + fileETag + `"`)
	if err != nil {
		t.Fatal(err)
	}
	if m := got.(map[string]any); m["restored"] != true || m["version_id"] != versionID {
		t.Errorf("result = %v", got)
	}
	if len(*moves) != 1 {
		t.Fatalf("moves = %d", len(*moves))
	}
	move := (*moves)[0]
	if move.URL.Path != versionFolder+"/"+versionID || move.Header.Get("Destination") != mainInstance+"/remote.php/dav/versions/"+aliceUser+"/restore/target" {
		t.Errorf("MOVE %s Destination %q", move.URL.Path, move.Header.Get("Destination"))
	}
	// Two stats and exactly one MOVE overall: one stat per call, the refused call sent no MOVE.
	var methods []string
	for _, c := range *calls {
		methods = append(methods, c.method)
	}
	if got := strings.Join(methods, " "); got != "PROPFIND PROPFIND MOVE" {
		t.Errorf("requests = %q", got)
	}
}

func TestVersionsUnclearRestoreIsNotRepeated(t *testing.T) {
	answers := map[string]func() (*http.Response, error){
		"500":     func() (*http.Response, error) { return status(500), nil },
		"504":     func() (*http.Response, error) { return status(504), nil },
		"timeout": func() (*http.Response, error) { return nil, timeoutError{} },
		"reset":   func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
	}
	for name, answer := range answers {
		t.Run(name, func(t *testing.T) {
			calls, moves := versionServer(t, ownList(), answer)
			red := &redact.Redactor{}
			_, err := invokeVersionsRestore(capability.WithConfirmed(context.Background()), localConnection("", ""), resolver(red), red,
				json.RawMessage(`{"path":"note.txt","version_id":"`+versionID+`","etag":"`+fileETag+`"}`))
			if err == nil || !strings.Contains(err.Error(), "may have been restored") || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("err = %v", err)
			}
			if len(*moves) != 1 || len(*calls) != 2 {
				t.Errorf("moves = %d, calls = %d", len(*moves), len(*calls))
			}
		})
	}
	for _, code := range []int{400, 403, 404, 409, 412} {
		_, _ = versionServer(t, ownList(), func() (*http.Response, error) { return status(code), nil })
		red := &redact.Redactor{}
		_, err := invokeVersionsRestore(capability.WithConfirmed(context.Background()), localConnection("", ""), resolver(red), red,
			json.RawMessage(`{"path":"note.txt","version_id":"`+versionID+`","etag":"`+fileETag+`"}`))
		if err == nil || strings.Contains(err.Error(), "may have been") {
			t.Errorf("%d: err = %v", code, err)
		}
	}
}

func TestVersionsNeverFollowRedirects(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		for _, step := range []string{"list", "get", "restore"} {
			t.Run(strconv.Itoa(code)+" "+step, func(t *testing.T) {
				var methods []string
				serve(t, func(request *http.Request) (*http.Response, error) {
					methods = append(methods, request.Method)
					if strings.Contains(request.URL.Path, "outside-canary") {
						t.Errorf("the redirect was followed: %s", request.URL)
					}
					if request.URL.Path == versionFolder || strings.HasPrefix(request.URL.Path, versionFolder+"/") || request.Method == methodMove {
						return &http.Response{StatusCode: code, Header: http.Header{"Location": {mainInstance + redirectTarget}},
							Body: io.NopCloser(strings.NewReader(bodyCanary))}, nil
					}
					return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
				})
				red := &redact.Redactor{}
				ctx := capability.WithConfirmed(context.Background())
				conn := localConnection("", "")
				var err error
				switch step {
				case "list":
					_, err = invokeVersionsList(ctx, conn, resolver(red), red, json.RawMessage(`{"path":"note.txt"}`))
				case "get":
					_, err = invokeVersionsGet(ctx, conn, resolver(red), red, json.RawMessage(`{"path":"note.txt","version_id":"`+versionID+`"}`))
				default:
					_, err = invokeVersionsRestore(ctx, conn, resolver(red), red, json.RawMessage(`{"path":"note.txt","version_id":"`+versionID+`","etag":"`+fileETag+`"}`))
				}
				if err == nil || !strings.Contains(err.Error(), "redirect") {
					t.Fatalf("err = %v", err)
				}
				for _, bad := range []string{redirectTarget, "outside-canary", bodyCanary, "may have been", aliceToken} {
					if strings.Contains(red.Error(err), bad) {
						t.Errorf("the error carries %q: %v", bad, err)
					}
				}
				if len(methods) != 2 {
					t.Errorf("requests = %v", methods)
				}
			})
		}
	}
}

func TestVersionsRisksProfileAndGroup(t *testing.T) {
	reg := registry(t)
	want := map[string]capability.Effect{"nextcloud.versions.list": capability.EffectRead,
		"nextcloud.versions.get": capability.EffectRead, "nextcloud.versions.restore": capability.EffectUpdate}
	for _, d := range reg.Provider(Provider) {
		effect, ok := want[d.ID]
		if !ok {
			continue
		}
		delete(want, d.ID)
		r := d.Risk
		if r.Effect != effect || r.Idempotency == "" || r.Confirmation == "" || !r.OpenWorld || r.DataSensitivity != dataSensitivity ||
			d.Group != "files" || d.RequiresToolAllowList {
			t.Errorf("%s = %+v", d.ID, d)
		}
		if d.ID == "nextcloud.versions.restore" && r.Confirmation != capability.ConfirmationRequired {
			t.Errorf("restore confirmation = %q", r.Confirmation)
		}
	}
	if len(want) != 0 {
		t.Errorf("not registered: %v", want)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		has := false
		for _, id := range p.Tools {
			has = has || id == versionsList.ID
			if id == versionsGet.ID || id == versionsRestore.ID {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
		if p.ID == "read" && !has {
			t.Error("read profile lacks versions.list")
		}
	}
}

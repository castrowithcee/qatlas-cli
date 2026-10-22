package forge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	assetUUID    = "0b8a1f6e-3c2d-4e5f-9a7b-1c2d3e4f5a6b"
	assetContent = "hello"
	releaseURL   = "/api/v1/repos/acme/widget/releases/12"
)

// assetFixture is one asset as both products answer it; Forgejo adds its type.
func assetFixture(p *product, id int, uuid, address string) map[string]any {
	asset := map[string]any{"id": id, "name": "app.zip", "size": len(assetContent), "download_count": 2,
		"uuid": uuid, "browser_download_url": address, "created_at": "2030-01-01T00:00:00Z"}
	if p == forgejo {
		asset["type"] = "attachment"
	}
	return asset
}

func instanceAddress(uuid string) string { return "https://" + apiHost + "/attachments/" + uuid }

func encode(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// assetServer holds release 12 of acme/widget with assets 3 and 4; asset 5 exists on the instance but is not
// one of the release's. asset replaces the answer for asset 3, content the answer of its address.
type assetServer struct {
	t       *testing.T
	p       *product
	asset   map[string]any
	content func() *http.Response
}

func newAssetServer(t *testing.T, p *product) *assetServer {
	return &assetServer{t: t, p: p, asset: assetFixture(p, 3, assetUUID, instanceAddress(assetUUID))}
}

func (s *assetServer) handle(c call) *http.Response {
	release := map[string]any{}
	if err := json.Unmarshal([]byte(releaseFixture(s.p, "12", "v1.2.0")), &release); err != nil {
		s.t.Fatal(err)
	}
	other := assetFixture(s.p, 4, "1b8a1f6e-3c2d-4e5f-9a7b-1c2d3e4f5a6b", instanceAddress("1b8a1f6e-3c2d-4e5f-9a7b-1c2d3e4f5a6b"))
	release["assets"] = []any{s.asset, other}
	switch {
	case c.method == http.MethodGet && c.path == releaseURL:
		return jsonResponse(200, encode(s.t, release))
	case c.method == http.MethodGet && c.path == releaseURL+"/assets":
		return jsonResponse(200, encode(s.t, []any{s.asset, other}))
	case c.method == http.MethodGet && c.path == releaseURL+"/assets/3":
		return jsonResponse(200, encode(s.t, s.asset))
	case c.method == http.MethodGet && c.path == releaseURL+"/assets/5":
		return jsonResponse(200, encode(s.t, assetFixture(s.p, 5, assetUUID, instanceAddress(assetUUID))))
	case c.method == http.MethodGet && c.path == "/attachments/"+assetUUID:
		if s.content != nil {
			return s.content()
		}
		response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(assetContent)),
			ContentLength: int64(len(assetContent)), Header: http.Header{"Content-Type": {"application/zip"}}}
		return response
	case c.method == http.MethodPost && c.path == releaseURL+"/assets":
		return jsonResponse(201, encode(s.t, assetFixture(s.p, 7, assetUUID, instanceAddress(assetUUID))))
	case c.method == http.MethodPatch && c.path == releaseURL+"/assets/3":
		renamed := assetFixture(s.p, 3, assetUUID, instanceAddress(assetUUID))
		renamed["name"] = "renamed.zip"
		return jsonResponse(201, encode(s.t, renamed))
	case c.method == http.MethodDelete && c.path == releaseURL+"/assets/3":
		return jsonResponse(204, "")
	}
	return notFoundResponse()
}

// assetDirs are the directories the file connections release: read for uploads, write for downloads.
type assetDirs struct{ read, write, outside string }

// newAssetEnvironment is newEnvironment with two more connections bound to acme/widget: files releases the
// directories, files-delete names the asset deletion in its tools list.
func newAssetEnvironment(t *testing.T, p *product, handler func(call) *http.Response) (*environment, assetDirs) {
	t.Helper()
	dirs := assetDirs{read: t.TempDir(), write: t.TempDir(), outside: t.TempDir()}
	calls := []call{}
	serve(t, &calls, handler)
	reads := 0
	red := &redact.Redactor{}
	cfg := testConfig(p)
	files := config.Files{Read: []string{dirs.read}, Write: []string{dirs.write}}
	cfg.Connections["files"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget"}, Files: files}
	cfg.Connections["files-delete"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget"}, Tools: []string{p.id + ".release-assets.delete"}}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red,
		reads: &reads, calls: &calls, p: p}, dirs
}

func TestReleaseAssetReadsBindTheReleaseAndTheAsset(t *testing.T) {
	for _, p := range products {
		e, _ := newAssetEnvironment(t, p, newAssetServer(t, p).handle)
		result, err := e.invoke(t, releaseAssetsList, "repo", `{"release_id":12}`)
		assets, _ := result["assets"].([]any)
		if err != nil || len(assets) != 2 || result["repository"] != "acme/widget" || result["release_id"] != float64(12) {
			t.Fatalf("%s list: %v, %v", p.id, result, err)
		}
		first := assets[0].(map[string]any)
		if first["uuid"] != assetUUID || first["browser_download_url"] != instanceAddress(assetUUID) ||
			first["download_count"] != float64(2) {
			t.Errorf("%s list row = %v", p.id, first)
		}
		if _, ok := first["type"]; ok != (p == forgejo) {
			t.Errorf("%s list row type = %v", p.id, first)
		}
		result, err = e.invoke(t, releaseAssetsGet, "repo", `{"release_id":12,"attachment_id":3}`)
		if err != nil || result["id"] != float64(3) || result["name"] != "app.zip" {
			t.Fatalf("%s get: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + releaseURL, "GET " + releaseURL + "/assets",
			"GET " + releaseURL, "GET " + releaseURL + "/assets/3"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		// Asset 5 is not one of release 12's: only the release is read.
		before := len(*e.calls)
		if _, err := e.invoke(t, releaseAssetsGet, "repo", `{"release_id":12,"attachment_id":5}`); !isInvalidRequest(err) ||
			len(*e.calls) != before+1 {
			t.Errorf("%s: an asset of another release: %v, calls %v", p.id, err, methods(e))
		}
	}
}

func TestReleaseAssetAnswerWithAnotherIDIsRefused(t *testing.T) {
	for _, p := range products {
		server := newAssetServer(t, p)
		e, _ := newAssetEnvironment(t, p, func(c call) *http.Response {
			if c.path == releaseURL+"/assets/3" {
				return jsonResponse(200, encode(t, assetFixture(p, 9, assetUUID, instanceAddress(assetUUID))))
			}
			return server.handle(c)
		})
		_, err := e.invoke(t, releaseAssetsGet, "repo", `{"release_id":12,"attachment_id":3}`)
		if err == nil || isInvalidRequest(err) {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}

func TestReleaseAssetOfARepositoryOutsideTheTargetsIsRefusedBeforeTheSecret(t *testing.T) {
	for _, p := range products {
		e, dirs := newAssetEnvironment(t, p, newAssetServer(t, p).handle)
		file := filepath.Join(dirs.read, "app.zip")
		if err := os.WriteFile(file, []byte(assetContent), 0o600); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dirs.write, "app.zip")
		for tl, arguments := range map[*tool]string{
			releaseAssetsList:     `{"release_id":12}`,
			releaseAssetsGet:      `{"release_id":12,"attachment_id":3}`,
			releaseAssetsUpdate:   `{"release_id":12,"attachment_id":3,"name":"x"}`,
			releaseAssetsUpload:   `{"release_id":12,"name":"x","local_path":` + quote(file) + `}`,
			releaseAssetsDownload: `{"release_id":12,"attachment_id":3,"local_path":` + quote(out) + `}`,
		} {
			arguments = `{"repository":"secret-owner/hidden",` + arguments[1:]
			_, err := e.invoke(t, tl, "files", arguments)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") {
				t.Errorf("%s %s: %v", p.id, tl.action, err)
			}
		}
		_, err := e.invoke(t, releaseAssetsDelete, "files-delete",
			`{"repository":"secret-owner/hidden","release_id":12,"attachment_id":3}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") {
			t.Errorf("%s delete: %v", p.id, err)
		}
		if *e.reads != 0 || len(*e.calls) != 0 {
			t.Errorf("%s: reads %d, calls %d", p.id, *e.reads, len(*e.calls))
		}
		if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: a refused download left a file", p.id)
		}
	}
}

func quote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestReleaseAssetUploadStreamsTheReleasedFileUnderItsName(t *testing.T) {
	for _, p := range products {
		e, dirs := newAssetEnvironment(t, p, newAssetServer(t, p).handle)
		file := filepath.Join(dirs.read, "local-build-name.bin")
		if err := os.WriteFile(file, []byte(assetContent), 0o600); err != nil {
			t.Fatal(err)
		}
		arguments := `{"release_id":12,"name":"app 1.zip","local_path":` + quote(file) + `}`
		if _, err := e.invokeAs(t, releaseAssetsUpload, "files", arguments, false); err == nil || *e.reads != 0 ||
			len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed upload: %v, calls %d", p.id, err, len(*e.calls))
		}
		result, err := e.invoke(t, releaseAssetsUpload, "files", arguments)
		if err != nil || result["id"] != float64(7) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + releaseURL, "POST " + releaseURL + "/assets"}) {
			t.Fatalf("%s calls = %v", p.id, got)
		}
		post := (*e.calls)[1]
		if post.query != "name=app+1.zip" || !strings.HasPrefix(post.contentType, "multipart/form-data; boundary=") ||
			!strings.Contains(post.body, `name="attachment"; filename="app 1.zip"`) ||
			!strings.Contains(post.body, "\r\n\r\n"+assetContent+"\r\n--") || strings.Contains(post.body, "local-build-name") ||
			strings.Contains(post.body, "external_url") {
			t.Errorf("%s upload = %+v", p.id, post)
		}
		// The connection without released directories does not offer the tool.
		if _, err := e.invoke(t, releaseAssetsUpload, "repo", arguments); err == nil {
			t.Errorf("%s: upload without a released directory", p.id)
		}
	}
}

func TestReleaseAssetUploadRefusesBeforeTheSecret(t *testing.T) {
	previous := maxAssetBytes
	maxAssetBytes = 4
	t.Cleanup(func() { maxAssetBytes = previous })
	for _, p := range products {
		e, dirs := newAssetEnvironment(t, p, newAssetServer(t, p).handle)
		inside := filepath.Join(dirs.read, "app.zip")
		outside := filepath.Join(dirs.outside, "app.zip")
		for _, path := range []string{inside, outside} {
			if err := os.WriteFile(path, []byte(assetContent), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		for name, arguments := range map[string]string{
			"too large":    `{"release_id":12,"name":"app.zip","local_path":` + quote(inside) + `}`,
			"outside":      `{"release_id":12,"name":"app.zip","local_path":` + quote(outside) + `}`,
			"external_url": `{"release_id":12,"name":"app.zip","external_url":"https://elsewhere.example/app.zip"}`,
			"both":         `{"release_id":12,"name":"app.zip","local_path":` + quote(inside) + `,"external_url":"https://elsewhere.example/a"}`,
			"name":         `{"release_id":12,"name":"bad\nname","local_path":` + quote(inside) + `}`,
			"no name":      `{"release_id":12,"local_path":` + quote(inside) + `}`,
		} {
			_, err := e.invoke(t, releaseAssetsUpload, "files", arguments)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), dirs.read) || strings.Contains(err.Error(), dirs.outside) {
				t.Errorf("%s %s: %v", p.id, name, err)
			}
		}
		if *e.reads != 0 || len(*e.calls) != 0 {
			t.Errorf("%s: reads %d, calls %d", p.id, *e.reads, len(*e.calls))
		}
	}
}

func TestReleaseAssetUploadWithAnOpenOutcomeIsNeverRepeated(t *testing.T) {
	for _, p := range products {
		server := newAssetServer(t, p)
		e, dirs := newAssetEnvironment(t, p, func(c call) *http.Response {
			if c.method == http.MethodPost {
				return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
			}
			return server.handle(c)
		})
		file := filepath.Join(dirs.read, "app.zip")
		if err := os.WriteFile(file, []byte(assetContent), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := e.invoke(t, releaseAssetsUpload, "files", `{"release_id":12,"name":"app.zip","local_path":`+quote(file)+`}`)
		if err == nil || !strings.Contains(err.Error(), "may have been applied") ||
			!reflect.DeepEqual(methods(e), []string{"GET " + releaseURL, "POST " + releaseURL + "/assets"}) {
			t.Errorf("%s: %v, calls %v", p.id, err, methods(e))
		}
	}
}

func TestReleaseAssetDownloadWritesTheInstanceAddressOnly(t *testing.T) {
	for _, p := range products {
		e, dirs := newAssetEnvironment(t, p, newAssetServer(t, p).handle)
		out := filepath.Join(dirs.write, "app.zip")
		arguments := `{"release_id":12,"attachment_id":3,"local_path":` + quote(out) + `}`
		result, err := e.invokeAs(t, releaseAssetsDownload, "files", arguments, false)
		sum := sha256.Sum256([]byte(assetContent))
		if err != nil || result["size"] != float64(len(assetContent)) || result["sha256"] != hex.EncodeToString(sum[:]) ||
			result["local_path"] != out || result["name"] != "app.zip" || result["attachment_id"] != float64(3) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if data, err := os.ReadFile(out); err != nil || string(data) != assetContent {
			t.Errorf("%s: file = %q, %v", p.id, data, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + releaseURL, "GET " + releaseURL + "/assets/3",
			"GET /attachments/" + assetUUID}) || (*e.calls)[2].auth != "token "+tokenValue {
			t.Errorf("%s calls = %v", p.id, got)
		}
		// The file exists now: replacing it needs confirmation, refused before the secret.
		reads, calls := *e.reads, len(*e.calls)
		_, err = e.invokeAs(t, releaseAssetsDownload, "files", arguments, false)
		var confirm *application.ConfirmationRequiredError
		if !errors.As(err, &confirm) || *e.reads != reads || len(*e.calls) != calls {
			t.Errorf("%s: overwrite without confirmation: %v", p.id, err)
		}
		if _, err := e.invoke(t, releaseAssetsDownload, "files", arguments); err != nil {
			t.Errorf("%s: confirmed overwrite: %v", p.id, err)
		}
		// A path outside the released directory is refused before the secret, without naming it.
		reads, calls = *e.reads, len(*e.calls)
		outside := filepath.Join(dirs.outside, "app.zip")
		_, err = e.invoke(t, releaseAssetsDownload, "files", `{"release_id":12,"attachment_id":3,"local_path":`+quote(outside)+`}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), dirs.outside) || *e.reads != reads || len(*e.calls) != calls {
			t.Errorf("%s: outside: %v", p.id, err)
		}
	}
}

func TestReleaseAssetDownloadRefusesEveryOtherAddress(t *testing.T) {
	const address = "browser_download_url"
	// Each case replaces members of asset 3; a nil value removes the member.
	cases := map[string]map[string]any{
		"other host":   {address: "https://cdn.example.test/attachments/" + assetUUID},
		"other port":   {address: "https://" + apiHost + ":8443/attachments/" + assetUUID},
		"http":         {address: "http://" + apiHost + "/attachments/" + assetUUID},
		"api path":     {address: "https://" + apiHost + "/api/v1/user"},
		"release path": {address: "https://" + apiHost + "/acme/widget/releases/download/v1.2.0/app.zip"},
		"query":        {address: instanceAddress(assetUUID) + "?x=1"},
		"other uuid":   {address: instanceAddress("2b8a1f6e-3c2d-4e5f-9a7b-1c2d3e4f5a6b")},
		"no uuid":      {"uuid": nil},
		"bad uuid":     {"uuid": "../x", address: instanceAddress("../x")},
		"external":     {"type": "external"},
	}
	for _, p := range products {
		for name, members := range cases {
			server := newAssetServer(t, p)
			for key, value := range members {
				if value == nil {
					delete(server.asset, key)
				} else {
					server.asset[key] = value
				}
			}
			e, dirs := newAssetEnvironment(t, p, server.handle)
			out := filepath.Join(dirs.write, "app.zip")
			_, err := e.invoke(t, releaseAssetsDownload, "files", `{"release_id":12,"attachment_id":3,"local_path":`+quote(out)+`}`)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "cdn.example") {
				t.Errorf("%s %s: %v", p.id, name, err)
			}
			if slices.ContainsFunc(*e.calls, func(c call) bool { return !strings.HasPrefix(c.path, "/api/v1/") }) {
				t.Errorf("%s %s: content requested: %v", p.id, name, methods(e))
			}
			if entries, _ := os.ReadDir(dirs.write); len(entries) != 0 {
				t.Errorf("%s %s: files left: %v", p.id, name, entries)
			}
		}
	}
}

func TestReleaseAssetDownloadFollowsNoRedirectAndHoldsTheSize(t *testing.T) {
	previous := maxAssetBytes
	t.Cleanup(func() { maxAssetBytes = previous })
	cases := map[string]struct {
		content func() *http.Response
		limit   int64
	}{
		"redirect": {content: func() *http.Response {
			return &http.Response{StatusCode: http.StatusFound, Body: io.NopCloser(strings.NewReader("")),
				Header: http.Header{"Location": {"https://storage.example.test/signed"}}}
		}},
		"longer": {content: func() *http.Response {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(assetContent + "!")), ContentLength: -1}
		}},
		"announced": {content: func() *http.Response {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(assetContent)), ContentLength: 9}
		}},
		"shorter": {content: func() *http.Response {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("hey")), ContentLength: -1}
		}},
		"refused": {content: notFoundResponse},
		"limit":   {limit: 4},
	}
	for _, p := range products {
		for name, tc := range cases {
			maxAssetBytes = previous
			if tc.limit != 0 {
				maxAssetBytes = tc.limit
			}
			server := newAssetServer(t, p)
			server.content = tc.content
			e, dirs := newAssetEnvironment(t, p, server.handle)
			out := filepath.Join(dirs.write, "app.zip")
			_, err := e.invoke(t, releaseAssetsDownload, "files", `{"release_id":12,"attachment_id":3,"local_path":`+quote(out)+`}`)
			if err == nil || strings.Contains(err.Error(), "storage.example") {
				t.Errorf("%s %s: %v", p.id, name, err)
			}
			if entries, _ := os.ReadDir(dirs.write); len(entries) != 0 {
				t.Errorf("%s %s: files left: %v", p.id, name, entries)
			}
			if name == "limit" && slices.ContainsFunc(*e.calls, func(c call) bool { return c.path == "/attachments/"+assetUUID }) {
				t.Errorf("%s: content of an asset above the limit requested", p.id)
			}
		}
	}
}

func TestReleaseAssetUpdateRenamesOnlyAListedAsset(t *testing.T) {
	for _, p := range products {
		e, _ := newAssetEnvironment(t, p, newAssetServer(t, p).handle)
		arguments := `{"release_id":12,"attachment_id":3,"name":"renamed.zip"}`
		if _, err := e.invokeAs(t, releaseAssetsUpdate, "repo", arguments, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed update: %v", p.id, err)
		}
		result, err := e.invoke(t, releaseAssetsUpdate, "repo", arguments)
		if err != nil || result["name"] != "renamed.zip" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + releaseURL, "PATCH " + releaseURL + "/assets/3"}) ||
			(*e.calls)[1].body != `{"name":"renamed.zip"}` {
			t.Errorf("%s calls = %v, body %s", p.id, got, (*e.calls)[1].body)
		}
		for _, refused := range []string{`{"release_id":12,"attachment_id":5,"name":"x"}`,
			`{"release_id":12,"attachment_id":3,"name":"x","browser_download_url":"https://elsewhere.example/a"}`} {
			before := len(*e.calls)
			_, err := e.invoke(t, releaseAssetsUpdate, "repo", refused)
			if !isInvalidRequest(err) || slices.ContainsFunc((*e.calls)[before:], func(c call) bool { return c.method != http.MethodGet }) {
				t.Errorf("%s %s: %v, calls %v", p.id, refused, err, methods(e))
			}
		}
	}
}

func TestReleaseAssetDeleteIsListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		d, _, ok := reg.Lookup(p.toolID(releaseAssetsDelete))
		if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
			t.Fatalf("%s descriptor = %+v", p.id, d)
		}
		for _, profile := range metadata.Profiles {
			if slices.Contains(profile.Tools, d.ID) {
				t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
			}
		}
		for _, tl := range []*tool{releaseAssetsList, releaseAssetsGet, releaseAssetsDownload, releaseAssetsUpload,
			releaseAssetsUpdate} {
			if d, _, _ := reg.Lookup(p.toolID(tl)); d.RequiresToolAllowList {
				t.Errorf("%s is listed-only", d.ID)
			}
		}
		up, _, _ := reg.Lookup(p.toolID(releaseAssetsUpload))
		down, _, _ := reg.Lookup(p.toolID(releaseAssetsDownload))
		if up.LocalFiles != config.LocalFilesRead || down.LocalFiles != config.LocalFilesWrite ||
			strings.Contains(string(up.InputSchema), "external_url") {
			t.Errorf("%s: upload %s, download %s", p.id, up.LocalFiles, down.LocalFiles)
		}
		e, _ := newAssetEnvironment(t, p, newAssetServer(t, p).handle)
		arguments := `{"release_id":12,"attachment_id":3}`
		if _, err := e.invoke(t, releaseAssetsDelete, "repo", arguments); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, releaseAssetsDelete, "files-delete", arguments, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		if _, err := e.invoke(t, releaseAssetsDelete, "files-delete", `{"release_id":12,"attachment_id":5}`); !isInvalidRequest(err) {
			t.Errorf("%s: delete of an asset of another release: %v", p.id, err)
		}
		result, err := e.invoke(t, releaseAssetsDelete, "files-delete", arguments)
		if err != nil || result["deleted"] != true || result["attachment_id"] != float64(3) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + releaseURL, "GET " + releaseURL,
			"DELETE " + releaseURL + "/assets/3"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
	}
}

func TestReleaseAssetOfAReleaseOfAnotherRepositoryIsRefused(t *testing.T) {
	for _, p := range products {
		server := newAssetServer(t, p)
		e, _ := newAssetEnvironment(t, p, func(c call) *http.Response {
			if c.path == releaseURL {
				return jsonResponse(200, releaseWithURL(p, "12", "https://"+apiHost+"/api/v1/repos/other/place/releases/12"))
			}
			return server.handle(c)
		})
		for tl, arguments := range map[*tool]string{releaseAssetsList: `{"release_id":12}`,
			releaseAssetsGet: `{"release_id":12,"attachment_id":1}`, releaseAssetsUpdate: `{"release_id":12,"attachment_id":1,"name":"x"}`} {
			before := len(*e.calls)
			_, err := e.invoke(t, tl, "repo", arguments)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "place") || len(*e.calls) != before+1 {
				t.Errorf("%s %s: %v, calls %v", p.id, tl.action, err, methods(e))
			}
		}
	}
}

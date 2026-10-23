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
)

// attachmentSubject is one owner kind of attachments under test: owner 7 (an issue) or 42 (a comment) of
// acme/widget lists attachment 1; attachment 2 exists on the instance but belongs to nobody there; foreign is
// an owner of another repository.
type attachmentSubject struct {
	owner                                       attachmentOwner
	list, get, upload, update, remove, download *tool
	id, foreign, key, base                      string
}

var attachmentSubjects = []attachmentSubject{
	{owner: issueAttachmentOwner, list: issueAttachmentsList, get: issueAttachmentsGet, upload: issueAttachmentsUpload,
		update: issueAttachmentsUpdate, remove: issueAttachmentsDelete, download: issueAttachmentsDownload,
		id: "7", foreign: "9", key: "number", base: "/api/v1/repos/acme/widget/issues/7"},
	{owner: commentAttachmentOwner, list: commentAttachmentsList, get: commentAttachmentsGet,
		upload: commentAttachmentsUpload, update: commentAttachmentsUpdate, remove: commentAttachmentsDelete,
		download: commentAttachmentsDownload, id: "42", foreign: "44", key: "id",
		base: "/api/v1/repos/acme/widget/issues/comments/42"},
}

type attachmentServer struct {
	t       *testing.T
	p       *product
	subject attachmentSubject
	asset   map[string]any
	content func() *http.Response
}

func newAttachmentServer(t *testing.T, p *product, s attachmentSubject) *attachmentServer {
	return &attachmentServer{t: t, p: p, subject: s, asset: assetFixture(p, 1, assetUUID, instanceAddress(assetUUID))}
}

// handle answers the owner (with the asset in its list), the attachment routes, and the content address.
func (s *attachmentServer) handle(c call) *http.Response {
	assets := "/assets"
	base := s.subject.base
	switch {
	case c.method == http.MethodGet && c.path == base:
		body := widgetIssue(s.p, 7, "open")
		if s.subject.key == "id" {
			body = commentFixture(42, "repos/acme/widget/issues/7")
		}
		owner := map[string]any{}
		if err := json.Unmarshal([]byte(body), &owner); err != nil {
			s.t.Fatal(err)
		}
		owner["assets"] = []any{s.asset}
		return jsonResponse(200, encode(s.t, owner))
	case c.method == http.MethodGet && c.path == base+assets:
		return jsonResponse(200, encode(s.t, []any{s.asset}))
	case c.method == http.MethodGet && c.path == base+assets+"/1":
		return jsonResponse(200, encode(s.t, s.asset))
	case c.method == http.MethodGet && c.path == base+assets+"/2":
		return jsonResponse(200, encode(s.t, assetFixture(s.p, 2, assetUUID, instanceAddress(assetUUID))))
	case c.method == http.MethodGet && c.path == "/attachments/"+assetUUID:
		if s.content != nil {
			return s.content()
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(assetContent)),
			ContentLength: int64(len(assetContent)), Header: http.Header{"Content-Type": {"text/plain"}}}
	case c.method == http.MethodPost && c.path == base+assets:
		return jsonResponse(201, encode(s.t, assetFixture(s.p, 7, assetUUID, instanceAddress(assetUUID))))
	case c.method == http.MethodPatch && c.path == base+assets+"/1":
		renamed := assetFixture(s.p, 1, assetUUID, instanceAddress(assetUUID))
		renamed["name"] = "renamed.zip"
		return jsonResponse(201, encode(s.t, renamed))
	case c.method == http.MethodDelete && c.path == base+assets+"/1":
		return jsonResponse(204, "")
	}
	// Owners of other repositories and the other owner kind answer as the issue and comment servers do.
	if response := issueServer(s.p, "open", nil)(c); response.StatusCode != http.StatusNotFound {
		return response
	}
	return commentServer(nil)(c)
}

func (s attachmentSubject) args(more string) string {
	return `{"` + s.key + `":` + s.id + more + `}`
}

func (s attachmentSubject) with(id, more string) string {
	return `{"` + s.key + `":` + id + more + `}`
}

func TestAttachmentReadsBindTheOwnerAndTheAttachment(t *testing.T) {
	for _, p := range products {
		for _, s := range attachmentSubjects {
			e, _ := newAssetEnvironment(t, p, newAttachmentServer(t, p, s).handle)
			result, err := e.invoke(t, s.list, "repo", s.args(``))
			rows, _ := result["attachments"].([]any)
			if err != nil || len(rows) != 1 || result["repository"] != "acme/widget" || result[s.key] == nil {
				t.Fatalf("%s %s list: %v, %v", p.id, s.owner.group, result, err)
			}
			first := rows[0].(map[string]any)
			if first["uuid"] != assetUUID || first["download_count"] != float64(2) {
				t.Errorf("%s %s row = %v", p.id, s.owner.group, first)
			}
			if _, ok := first["type"]; ok != (p == forgejo) {
				t.Errorf("%s %s row type = %v", p.id, s.owner.group, first)
			}
			result, err = e.invoke(t, s.get, "repo", s.args(`,"attachment_id":1`))
			if err != nil || result["id"] != float64(1) || result["name"] != "app.zip" {
				t.Fatalf("%s %s get: %v, %v", p.id, s.owner.group, result, err)
			}
			want := []string{"GET " + s.base, "GET " + s.base + "/assets", "GET " + s.base,
				"GET " + s.base + "/assets/1"}
			if got := methods(e); !reflect.DeepEqual(got, want) {
				t.Errorf("%s %s calls = %v, want %v", p.id, s.owner.group, got, want)
			}
			// Attachment 2 exists but the owner does not list it: only the owner is read.
			before := len(*e.calls)
			if _, err := e.invoke(t, s.get, "repo", s.args(`,"attachment_id":2`)); !isInvalidRequest(err) ||
				len(*e.calls) != before+1 {
				t.Errorf("%s %s: an attachment of another owner: %v, calls %v", p.id, s.owner.group, err, methods(e))
			}
			// An answer with another ID is not taken for the attachment.
			before = len(*e.calls)
			if _, err := e.invoke(t, s.get, "repo", s.with(s.foreign, `,"attachment_id":1`)); !isInvalidRequest(err) ||
				len(*e.calls) != before+1 {
				t.Errorf("%s %s: an owner of another repository: %v, calls %v", p.id, s.owner.group, err, methods(e))
			}
		}
	}
}

func (s attachmentSubject) write(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(assetContent), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAttachmentOfAnOwnerOutsideTheTargetsIsRefusedBeforeTheSecret(t *testing.T) {
	for _, p := range products {
		for _, s := range attachmentSubjects {
			e, dirs := newAssetEnvironment(t, p, newAttachmentServer(t, p, s).handle)
			file := s.write(t, dirs.read, "log.txt")
			out := filepath.Join(dirs.write, "log.txt")
			for tl, arguments := range map[*tool]string{
				s.list:     s.args(``),
				s.get:      s.args(`,"attachment_id":1`),
				s.update:   s.args(`,"attachment_id":1,"name":"x"`),
				s.upload:   s.args(`,"name":"x","local_path":` + quote(file)),
				s.download: s.args(`,"attachment_id":1,"local_path":` + quote(out)),
			} {
				arguments = `{"repository":"secret-owner/hidden",` + arguments[1:]
				_, err := e.invoke(t, tl, "files", arguments)
				if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") {
					t.Errorf("%s %s %s: %v", p.id, s.owner.group, tl.action, err)
				}
			}
			_, err := e.invoke(t, s.remove, "files-delete", `{"repository":"secret-owner/hidden",`+s.args(`,"attachment_id":1`)[1:])
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") {
				t.Errorf("%s %s delete: %v", p.id, s.owner.group, err)
			}
			if *e.reads != 0 || len(*e.calls) != 0 {
				t.Errorf("%s %s: reads %d, calls %d", p.id, s.owner.group, *e.reads, len(*e.calls))
			}
			// An owner whose evidence names another repository stops every tool at the evidence read.
			for tl, arguments := range map[*tool]string{
				s.list:     s.with(s.foreign, ``),
				s.update:   s.with(s.foreign, `,"attachment_id":1,"name":"x"`),
				s.upload:   s.with(s.foreign, `,"name":"x","local_path":`+quote(file)),
				s.download: s.with(s.foreign, `,"attachment_id":1,"local_path":`+quote(out)),
			} {
				before := len(*e.calls)
				_, err := e.invoke(t, tl, "files", arguments)
				if !isInvalidRequest(err) || len(*e.calls) != before+1 || (*e.calls)[before].method != http.MethodGet {
					t.Errorf("%s %s %s: %v, calls %v", p.id, s.owner.group, tl.action, err, methods(e))
				}
			}
			before := len(*e.calls)
			_, err = e.invoke(t, s.remove, "files-delete", s.with(s.foreign, `,"attachment_id":1`))
			if !isInvalidRequest(err) || len(*e.calls) != before+1 {
				t.Errorf("%s %s delete: %v, calls %v", p.id, s.owner.group, err, methods(e))
			}
			if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s %s: a refused download left a file", p.id, s.owner.group)
			}
		}
	}
}

func TestAttachmentUploadStreamsTheReleasedFileUnderItsName(t *testing.T) {
	for _, p := range products {
		for _, s := range attachmentSubjects {
			e, dirs := newAssetEnvironment(t, p, newAttachmentServer(t, p, s).handle)
			file := s.write(t, dirs.read, "local-name.bin")
			arguments := s.args(`,"name":"log 1.txt","local_path":` + quote(file))
			if _, err := e.invokeAs(t, s.upload, "files", arguments, false); err == nil || *e.reads != 0 ||
				len(*e.calls) != 0 {
				t.Errorf("%s %s: unconfirmed upload: %v", p.id, s.owner.group, err)
			}
			result, err := e.invoke(t, s.upload, "files", arguments)
			if err != nil || result["id"] != float64(7) {
				t.Fatalf("%s %s: %v, %v", p.id, s.owner.group, result, err)
			}
			if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + s.base, "POST " + s.base + "/assets"}) {
				t.Fatalf("%s %s calls = %v", p.id, s.owner.group, got)
			}
			post := (*e.calls)[1]
			if post.query != "name=log+1.txt" || !strings.HasPrefix(post.contentType, "multipart/form-data; boundary=") ||
				!strings.Contains(post.body, `name="attachment"; filename="log 1.txt"`) ||
				!strings.Contains(post.body, "\r\n\r\n"+assetContent+"\r\n--") || strings.Contains(post.body, "local-name") ||
				strings.Contains(post.body, "browser_download_url") || strings.Contains(post.query, "updated_at") {
				t.Errorf("%s %s upload = %+v", p.id, s.owner.group, post)
			}
			if _, err := e.invoke(t, s.upload, "repo", arguments); err == nil {
				t.Errorf("%s %s: upload without a released directory", p.id, s.owner.group)
			}
		}
	}
}

func TestAttachmentUploadRefusesBeforeTheSecret(t *testing.T) {
	previous := maxAssetBytes
	maxAssetBytes = 4
	t.Cleanup(func() { maxAssetBytes = previous })
	for _, p := range products {
		for _, s := range attachmentSubjects {
			e, dirs := newAssetEnvironment(t, p, newAttachmentServer(t, p, s).handle)
			inside := s.write(t, dirs.read, "log.txt")
			outside := s.write(t, dirs.outside, "log.txt")
			for name, arguments := range map[string]string{
				"too large":     s.args(`,"name":"log.txt","local_path":` + quote(inside)),
				"outside":       s.args(`,"name":"log.txt","local_path":` + quote(outside)),
				"external":      s.args(`,"name":"log.txt","external_url":"https://elsewhere.example/a"`),
				"updated_at":    s.args(`,"name":"log.txt","local_path":` + quote(inside) + `,"updated_at":"2030-01-01T00:00:00Z"`),
				"name":          s.args(`,"name":"bad\nname","local_path":` + quote(inside)),
				"no name":       s.args(`,"local_path":` + quote(inside)),
				"no local_path": s.args(`,"name":"log.txt"`),
			} {
				_, err := e.invoke(t, s.upload, "files", arguments)
				if !isInvalidRequest(err) || strings.Contains(err.Error(), dirs.read) || strings.Contains(err.Error(), dirs.outside) {
					t.Errorf("%s %s %s: %v", p.id, s.owner.group, name, err)
				}
			}
			if *e.reads != 0 || len(*e.calls) != 0 {
				t.Errorf("%s %s: reads %d, calls %d", p.id, s.owner.group, *e.reads, len(*e.calls))
			}
		}
	}
}

func TestAttachmentUploadWithAnOpenOutcomeIsNeverRepeated(t *testing.T) {
	for _, p := range products {
		for _, s := range attachmentSubjects {
			server := newAttachmentServer(t, p, s)
			e, dirs := newAssetEnvironment(t, p, func(c call) *http.Response {
				if c.method == http.MethodPost {
					return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
				}
				return server.handle(c)
			})
			file := s.write(t, dirs.read, "log.txt")
			_, err := e.invoke(t, s.upload, "files", s.args(`,"name":"log.txt","local_path":`+quote(file)))
			if err == nil || !strings.Contains(err.Error(), "may have been applied") ||
				!reflect.DeepEqual(methods(e), []string{"GET " + s.base, "POST " + s.base + "/assets"}) {
				t.Errorf("%s %s: %v, calls %v", p.id, s.owner.group, err, methods(e))
			}
		}
	}
}

func TestAttachmentDownloadWritesTheInstanceAddressOnly(t *testing.T) {
	for _, p := range products {
		for _, s := range attachmentSubjects {
			e, dirs := newAssetEnvironment(t, p, newAttachmentServer(t, p, s).handle)
			out := filepath.Join(dirs.write, "log.txt")
			arguments := s.args(`,"attachment_id":1,"local_path":` + quote(out))
			result, err := e.invokeAs(t, s.download, "files", arguments, false)
			sum := sha256.Sum256([]byte(assetContent))
			if err != nil || result["size"] != float64(len(assetContent)) || result["sha256"] != hex.EncodeToString(sum[:]) ||
				result["local_path"] != out || result["name"] != "app.zip" || result["attachment_id"] != float64(1) ||
				result[s.key] == nil || result["content"] != nil {
				t.Fatalf("%s %s: %v, %v", p.id, s.owner.group, result, err)
			}
			if data, err := os.ReadFile(out); err != nil || string(data) != assetContent {
				t.Errorf("%s %s: file = %q, %v", p.id, s.owner.group, data, err)
			}
			want := []string{"GET " + s.base, "GET " + s.base + "/assets/1", "GET /attachments/" + assetUUID}
			if got := methods(e); !reflect.DeepEqual(got, want) || (*e.calls)[2].auth != "token "+tokenValue {
				t.Errorf("%s %s calls = %v", p.id, s.owner.group, got)
			}
			reads, calls := *e.reads, len(*e.calls)
			_, err = e.invokeAs(t, s.download, "files", arguments, false)
			var confirm *application.ConfirmationRequiredError
			if !errors.As(err, &confirm) || *e.reads != reads || len(*e.calls) != calls {
				t.Errorf("%s %s: overwrite without confirmation: %v", p.id, s.owner.group, err)
			}
			if _, err := e.invoke(t, s.download, "files", arguments); err != nil {
				t.Errorf("%s %s: confirmed overwrite: %v", p.id, s.owner.group, err)
			}
			reads, calls = *e.reads, len(*e.calls)
			outside := filepath.Join(dirs.outside, "log.txt")
			_, err = e.invoke(t, s.download, "files", s.args(`,"attachment_id":1,"local_path":`+quote(outside)))
			if !isInvalidRequest(err) || strings.Contains(err.Error(), dirs.outside) || *e.reads != reads ||
				len(*e.calls) != calls {
				t.Errorf("%s %s: outside: %v", p.id, s.owner.group, err)
			}
			// Attachment 2 is not listed by the owner: nothing but the owner is requested.
			calls = len(*e.calls)
			_, err = e.invoke(t, s.download, "files", s.args(`,"attachment_id":2,"local_path":`+quote(filepath.Join(dirs.write, "b.txt"))))
			if !isInvalidRequest(err) || len(*e.calls) != calls+1 {
				t.Errorf("%s %s: foreign attachment: %v, calls %v", p.id, s.owner.group, err, methods(e))
			}
		}
	}
}

func TestAttachmentDownloadRefusesEveryOtherAddress(t *testing.T) {
	const address = "browser_download_url"
	cases := map[string]map[string]any{
		"other host": {address: "https://cdn.example.test/attachments/" + assetUUID},
		"other port": {address: "https://" + apiHost + ":8443/attachments/" + assetUUID},
		"http":       {address: "http://" + apiHost + "/attachments/" + assetUUID},
		"api path":   {address: "https://" + apiHost + "/api/v1/user"},
		"query":      {address: instanceAddress(assetUUID) + "?x=1"},
		"other uuid": {address: instanceAddress("2b8a1f6e-3c2d-4e5f-9a7b-1c2d3e4f5a6b")},
		"no uuid":    {"uuid": nil},
		"external":   {"type": "external"},
	}
	for _, p := range products {
		for _, s := range attachmentSubjects {
			for name, members := range cases {
				server := newAttachmentServer(t, p, s)
				for key, value := range members {
					if value == nil {
						delete(server.asset, key)
					} else {
						server.asset[key] = value
					}
				}
				e, dirs := newAssetEnvironment(t, p, server.handle)
				out := filepath.Join(dirs.write, "log.txt")
				_, err := e.invoke(t, s.download, "files", s.args(`,"attachment_id":1,"local_path":`+quote(out)))
				if !isInvalidRequest(err) || strings.Contains(err.Error(), "cdn.example") {
					t.Errorf("%s %s %s: %v", p.id, s.owner.group, name, err)
				}
				if slices.ContainsFunc(*e.calls, func(c call) bool { return !strings.HasPrefix(c.path, "/api/v1/") }) {
					t.Errorf("%s %s %s: content requested: %v", p.id, s.owner.group, name, methods(e))
				}
				if entries, _ := os.ReadDir(dirs.write); len(entries) != 0 {
					t.Errorf("%s %s %s: files left: %v", p.id, s.owner.group, name, entries)
				}
			}
		}
	}
}

func TestAttachmentDownloadFollowsNoRedirectAndHoldsTheSize(t *testing.T) {
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
		"refused": {content: notFoundResponse},
		"limit":   {limit: 4},
	}
	for _, p := range products {
		for _, s := range attachmentSubjects {
			for name, tc := range cases {
				maxAssetBytes = previous
				if tc.limit != 0 {
					maxAssetBytes = tc.limit
				}
				server := newAttachmentServer(t, p, s)
				server.content = tc.content
				e, dirs := newAssetEnvironment(t, p, server.handle)
				out := filepath.Join(dirs.write, "log.txt")
				_, err := e.invoke(t, s.download, "files", s.args(`,"attachment_id":1,"local_path":`+quote(out)))
				if err == nil || strings.Contains(err.Error(), "storage.example") {
					t.Errorf("%s %s %s: %v", p.id, s.owner.group, name, err)
				}
				if entries, _ := os.ReadDir(dirs.write); len(entries) != 0 {
					t.Errorf("%s %s %s: files left: %v", p.id, s.owner.group, name, entries)
				}
				if name == "limit" && slices.ContainsFunc(*e.calls, func(c call) bool { return c.path == "/attachments/"+assetUUID }) {
					t.Errorf("%s %s: content above the limit requested", p.id, s.owner.group)
				}
			}
		}
	}
}

func TestAttachmentUpdateRenamesOnlyAListedAttachment(t *testing.T) {
	for _, p := range products {
		for _, s := range attachmentSubjects {
			e, _ := newAssetEnvironment(t, p, newAttachmentServer(t, p, s).handle)
			arguments := s.args(`,"attachment_id":1,"name":"renamed.zip"`)
			if _, err := e.invokeAs(t, s.update, "repo", arguments, false); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s %s: unconfirmed update: %v", p.id, s.owner.group, err)
			}
			result, err := e.invoke(t, s.update, "repo", arguments)
			if err != nil || result["name"] != "renamed.zip" {
				t.Fatalf("%s %s: %v, %v", p.id, s.owner.group, result, err)
			}
			if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + s.base, "PATCH " + s.base + "/assets/1"}) ||
				(*e.calls)[1].body != `{"name":"renamed.zip"}` {
				t.Errorf("%s %s calls = %v, body %s", p.id, s.owner.group, got, (*e.calls)[1].body)
			}
			for _, refused := range []string{s.args(`,"attachment_id":2,"name":"x"`),
				s.args(`,"attachment_id":1,"name":"x","browser_download_url":"https://elsewhere.example/a"`)} {
				before := len(*e.calls)
				_, err := e.invoke(t, s.update, "repo", refused)
				if !isInvalidRequest(err) || slices.ContainsFunc((*e.calls)[before:], func(c call) bool { return c.method != http.MethodGet }) {
					t.Errorf("%s %s %s: %v, calls %v", p.id, s.owner.group, refused, err, methods(e))
				}
			}
		}
	}
}

func TestAttachmentDeleteIsListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, s := range attachmentSubjects {
			d, _, ok := reg.Lookup(p.toolID(s.remove))
			if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
				t.Fatalf("%s descriptor = %+v", p.id, d)
			}
			for _, profile := range metadata.Profiles {
				if slices.Contains(profile.Tools, d.ID) {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, d.ID)
				}
			}
			for _, tl := range []*tool{s.list, s.get, s.download, s.upload, s.update} {
				if other, _, _ := reg.Lookup(p.toolID(tl)); other.RequiresToolAllowList {
					t.Errorf("%s is listed-only", other.ID)
				}
			}
			up, _, _ := reg.Lookup(p.toolID(s.upload))
			down, _, _ := reg.Lookup(p.toolID(s.download))
			if up.LocalFiles != config.LocalFilesRead || down.LocalFiles != config.LocalFilesWrite ||
				strings.Contains(string(up.InputSchema), "external_url") || strings.Contains(string(up.InputSchema), "updated_at") {
				t.Errorf("%s: upload %s, download %s", p.id, up.LocalFiles, down.LocalFiles)
			}
			if update, _, _ := reg.Lookup(p.toolID(s.update)); strings.Contains(string(update.InputSchema), "browser_download_url") {
				t.Errorf("%s offers browser_download_url", update.ID)
			}
			e, _ := newAssetEnvironment(t, p, newAttachmentServer(t, p, s).handle)
			arguments := s.args(`,"attachment_id":1`)
			if _, err := e.invoke(t, s.remove, "repo", arguments); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s %s: delete without a tools list: %v", p.id, s.owner.group, err)
			}
			if _, err := e.invokeAs(t, s.remove, "files-delete", arguments, false); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s %s: unconfirmed delete: %v", p.id, s.owner.group, err)
			}
			if _, err := e.invoke(t, s.remove, "files-delete", s.args(`,"attachment_id":2`)); !isInvalidRequest(err) {
				t.Errorf("%s %s: delete of an attachment of another owner: %v", p.id, s.owner.group, err)
			}
			result, err := e.invoke(t, s.remove, "files-delete", arguments)
			if err != nil || result["deleted"] != true || result["attachment_id"] != float64(1) {
				t.Fatalf("%s %s: %v, %v", p.id, s.owner.group, result, err)
			}
			if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + s.base, "GET " + s.base,
				"DELETE " + s.base + "/assets/1"}) {
				t.Errorf("%s %s calls = %v", p.id, s.owner.group, got)
			}
		}
	}
}

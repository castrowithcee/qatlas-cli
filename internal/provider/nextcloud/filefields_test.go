package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const readExtraProps = `<d:creationdate/><nc:creation_time/><nc:upload_time/><d:getcontentlanguage/><oc:id/><oc:favorite/>` +
	`<ocs:share-permissions/><ocm:share-permissions/><oc:owner-id/><oc:owner-display-name/><oc:share-types/>` +
	`<nc:sharees/><nc:note/><nc:share-attributes/><nc:hide-download/><nc:mount-type/><nc:is-mount-root/>` +
	`<nc:is-federated/><nc:is-encrypted/><oc:comments-count/><oc:comments-unread/><oc:comments-href/><oc:tags/>` +
	`<oc:checksums/><nc:has-preview/><nc:hidden/><nc:metadata_etag/><nc:data-fingerprint/><oc:data-fingerprint/>` +
	`<d:quota-used-bytes/><d:quota-available-bytes/><nc:contained-file-count/><nc:contained-folder-count/>`

const fixedProps = `<d:displayname/><d:resourcetype/><d:getcontenttype/><d:getcontentlength/>` +
	`<d:getlastmodified/><d:getetag/><oc:fileid/><oc:size/><oc:permissions/>`

const fileNamespacesLiteral = `xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns" xmlns:nc="http://nextcloud.org/ns" ` +
	`xmlns:ocs="http://open-collaboration-services.org/ns" xmlns:ocm="http://open-cloud-mesh.org/ns"`

const fileFavoritesLiteral = `<?xml version="1.0" encoding="UTF-8"?><oc:filter-files ` + fileNamespacesLiteral +
	`><oc:filter-rules><oc:favorite>1</oc:favorite></oc:filter-rules><d:prop>` + fixedProps + readExtraProps +
	`</d:prop></oc:filter-files>`

func TestFileBodiesAskForTheFullSetWithDeclaredNamespaces(t *testing.T) {
	want := `<?xml version="1.0" encoding="UTF-8"?><d:propfind ` + fileNamespacesLiteral + `><d:prop>` +
		fixedProps + readExtraProps + `</d:prop></d:propfind>`
	if filePropfindBody != want {
		t.Errorf("propfind body =\n%s\nwant\n%s", filePropfindBody, want)
	}
	if fileFavoritesBody != fileFavoritesLiteral {
		t.Errorf("favorites body =\n%s", fileFavoritesBody)
	}
	for _, body := range []string{filePropfindBody, fileFavoritesBody, fileSearchProps} {
		for _, never := range []string{"downloadURL", "download-url-expiration", "lockdiscovery", "supportedlock"} {
			if strings.Contains(body, never) {
				t.Errorf("a body asks for %s", never)
			}
		}
	}
	// The fixed body of the writing tools and the version list is unchanged.
	if strings.Contains(propfindBody, "nc:") {
		t.Errorf("the fixed property body changed: %s", propfindBody)
	}
}

// fullFile answers every documented property, nested and repeated ones included, plus the properties
// that are never reported.
func fullFile(href string, folder bool) string {
	kind := "<d:resourcetype/>"
	extra := ""
	if folder {
		kind = "<d:resourcetype><d:collection/></d:resourcetype>"
		extra = `<d:quota-used-bytes>1234</d:quota-used-bytes><d:quota-available-bytes>-3</d:quota-available-bytes>` +
			`<nc:contained-file-count>7</nc:contained-file-count><nc:contained-folder-count>2</nc:contained-folder-count>`
	}
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop>` +
		`<d:displayname>q1.pdf</d:displayname>` + kind + `<d:getcontenttype>application/pdf</d:getcontenttype>` +
		`<d:getcontentlength>5120</d:getcontentlength><d:getlastmodified>Wed, 01 Apr 2026 07:05:00 GMT</d:getlastmodified>` +
		`<d:getetag>&quot;3a71bd0c1&quot;</d:getetag><oc:fileid>1003</oc:fileid><oc:size>5120</oc:size>` +
		`<oc:permissions>RGDNVW</oc:permissions>` +
		`<d:creationdate>2026-03-01T10:00:00+01:00</d:creationdate><nc:creation_time>1772355600</nc:creation_time>` +
		`<nc:upload_time>1772359200</nc:upload_time><d:getcontentlanguage>de</d:getcontentlanguage>` +
		`<oc:id>00001003ocabc</oc:id><oc:favorite>1</oc:favorite>` +
		`<ocs:share-permissions>19</ocs:share-permissions>` +
		`<ocm:share-permissions>["share","read","write"]</ocm:share-permissions>` +
		`<oc:owner-id>alice</oc:owner-id><oc:owner-display-name>Alice A.</oc:owner-display-name>` +
		`<oc:share-types>
			<oc:share-type>0</oc:share-type>
			<oc:share-type>3</oc:share-type>
		</oc:share-types>` +
		`<nc:sharees><nc:sharee><nc:id>bob</nc:id><nc:display-name>Bob B.</nc:display-name><nc:type>0</nc:type></nc:sharee>` +
		`<nc:sharee><nc:id>team</nc:id><nc:display-name>Team</nc:display-name><nc:type>1</nc:type></nc:sharee></nc:sharees>` +
		`<nc:note>Please review</nc:note>` +
		`<nc:share-attributes>[{"scope":"permissions","key":"download","value":false}]</nc:share-attributes>` +
		`<nc:hide-download>true</nc:hide-download><nc:mount-type>shared</nc:mount-type>` +
		`<nc:is-mount-root>false</nc:is-mount-root><nc:is-federated>false</nc:is-federated>` +
		`<nc:is-encrypted>false</nc:is-encrypted><oc:comments-count>3</oc:comments-count>` +
		`<oc:comments-unread>1</oc:comments-unread><oc:comments-href>/remote.php/dav/comments/files/1003</oc:comments-href>` +
		`<oc:tags><oc:tag>draft</oc:tag><oc:tag>q1</oc:tag></oc:tags>` +
		`<oc:checksums><oc:checksum>SHA1:aaaa MD5:bbbb</oc:checksum><oc:checksum>ADLER32:cccc</oc:checksum></oc:checksums>` +
		`<nc:has-preview>true</nc:has-preview><nc:hidden>false</nc:hidden><nc:metadata_etag>meta1</nc:metadata_etag>` +
		`<oc:data-fingerprint>fp1</oc:data-fingerprint><nc:last_activity>1772359300</nc:last_activity>` +
		extra +
		`<oc:downloadURL>https://secret.example/dl?token=abc</oc:downloadURL>` +
		`<nc:download-url-expiration>1772359999</nc:download-url-expiration>` +
		`<d:lockdiscovery/><d:supportedlock><d:lockentry/></d:supportedlock>` +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

func wantFullEntry(folder bool) Entry {
	yes, no := true, false
	var zero int64
	one, three, nineteen := int64(1), int64(3), int64(19)
	_ = zero
	zeroType, groupType := int64(0), int64(1)
	e := Entry{
		Path: "2026/q1.pdf", Name: "q1.pdf", Type: typeFile, ContentType: "application/pdf", Size: 5120,
		ModifiedAt: "2026-04-01T07:05:00Z", ETag: "3a71bd0c1", FileID: "1003", Readable: true,
		CreatedAt: "2026-03-01T09:00:00Z", CreationTime: 1772355600, UploadTime: 1772359200, ContentLanguage: "de",
		GlobalID: "00001003ocabc", Favorite: &yes, Permissions: "RGDNVW", SharePermissions: &nineteen,
		OCMSharePermissions: []string{"share", "read", "write"}, OwnerID: "alice", OwnerDisplayName: "Alice A.",
		ShareTypes: []int64{0, 3},
		Sharees: []FileSharee{{ID: "bob", DisplayName: "Bob B.", Type: &zeroType},
			{ID: "team", DisplayName: "Team", Type: &groupType}},
		Note:            "Please review",
		ShareAttributes: []any{map[string]any{"scope": "permissions", "key": "download", "value": false}},
		HideDownload:    &yes, MountType: "shared", IsMountRoot: &no, IsFederated: &no, IsEncrypted: &no,
		CommentsCount: &three, CommentsUnread: &one, CommentsHref: "/remote.php/dav/comments/files/1003",
		Tags: []string{"draft", "q1"}, Checksums: []string{"SHA1:aaaa", "MD5:bbbb", "ADLER32:cccc"},
		HasPreview: &yes, Hidden: &no, MetadataETag: "meta1", DataFingerprint: "fp1",
	}
	_ = one
	if folder {
		used, free, files, dirs := int64(1234), int64(-3), int64(7), int64(2)
		e.QuotaUsedBytes, e.QuotaAvailableBytes, e.ContainedFileCount, e.ContainedFolderCount = &used, &free, &files, &dirs
	}
	return e
}

func TestStatReportsEveryDocumentedProperty(t *testing.T) {
	for _, folder := range []bool{false, true} {
		serve(t, func(*http.Request) (*http.Response, error) {
			return xmlResponse(http.StatusMultiStatus, multistatus(fullFile(aliceRoot+"/2026/q1.pdf", folder))), nil
		})
		c, _ := client(t)
		got, err := c.StatFile(context.Background(), "2026/q1.pdf")
		if err != nil {
			t.Fatal(err)
		}
		want := wantFullEntry(folder)
		// last_activity is a search-only field and the quota fields belong to folders.
		if folder {
			want.Type = typeFolder
		}
		if folder {
			// the fixture marks the node as a folder through its resourcetype
			if got.Type != typeFolder {
				t.Fatalf("type = %s", got.Type)
			}
			want.ContentType, want.Size = "", 5120
		}
		if !reflect.DeepEqual(*got, want) {
			gj, _ := json.Marshal(got)
			wj, _ := json.Marshal(want)
			t.Errorf("folder=%v entry =\n%s\nwant\n%s", folder, gj, wj)
		}
		raw, _ := json.Marshal(got)
		for _, never := range []string{"secret.example", "1772359999", "lock", "last_activity"} {
			if strings.Contains(string(raw), never) {
				t.Errorf("the output carries %q: %s", never, raw)
			}
		}
	}
}

func TestListAndFavoritesReportTheFullEntrySearchTheSelectableSubset(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(
			folderXML(aliceRoot+"/", "Reports", "1001", "4096"), fullFile(aliceRoot+"/q1.pdf", false))), nil
	})
	c, _ := client(t)
	list, err := c.ListFiles(context.Background(), "")
	if err != nil || len(list.Entries) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	want := wantFullEntry(false)
	want.Path = "q1.pdf"
	if !reflect.DeepEqual(list.Entries[0], want) {
		t.Errorf("list entry = %+v", list.Entries[0])
	}

	serve(t, func(*http.Request) (*http.Response, error) {
		return searchAnswer(fullFile(aliceRoot+"/q1.pdf", false)), nil
	})
	c, _ = client(t)
	fav, err := c.ListFavorites(context.Background())
	if err != nil || !reflect.DeepEqual(fav.Entries[0], want) {
		t.Errorf("favorites = %+v, %v", fav, err)
	}
	search, err := c.SearchFiles(context.Background(), "search files",
		&searchRequest{conditions: []string{"<d:is-collection/>"}, limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	got := search.Entries[0]
	// Only what the search documents as selectable, and last_activity.
	yes := true
	sel := Entry{
		Path: "q1.pdf", Name: "q1.pdf", Type: typeFile, ContentType: "application/pdf", Size: 5120,
		ModifiedAt: "2026-04-01T07:05:00Z", ETag: "3a71bd0c1", FileID: "1003", Readable: true,
		CreatedAt: "2026-03-01T09:00:00Z", UploadTime: 1772359200, Favorite: &yes, Permissions: "RGDNVW",
		OwnerID: "alice", OwnerDisplayName: "Alice A.", Checksums: want.Checksums, HasPreview: &yes,
		DataFingerprint: "fp1", LastActivity: "1772359300",
	}
	if !reflect.DeepEqual(got, sel) {
		gj, _ := json.Marshal(got)
		t.Errorf("search entry = %s", gj)
	}
}

// An answer without the new properties yields the former output byte for byte.
func TestAnswerWithoutNewPropertiesKeepsTheFormerOutput(t *testing.T) {
	legacy := `<d:response><d:href>` + aliceRoot + `/q1.pdf</d:href><d:propstat><d:prop>` +
		`<d:displayname>q1.pdf</d:displayname><d:resourcetype/><d:getcontenttype>application/pdf</d:getcontenttype>` +
		`<d:getcontentlength>5120</d:getcontentlength><d:getlastmodified>Wed, 01 Apr 2026 07:05:00 GMT</d:getlastmodified>` +
		`<d:getetag>&quot;3a71bd0c1&quot;</d:getetag><oc:fileid>1003</oc:fileid>` +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(legacy)), nil
	})
	c, _ := client(t)
	got, err := c.StatFile(context.Background(), "q1.pdf")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	want := `{"path":"q1.pdf","name":"q1.pdf","type":"file","content_type":"application/pdf","size":5120,` +
		`"modified_at":"2026-04-01T07:05:00Z","etag":"3a71bd0c1","file_id":"1003","readable":true}`
	if string(raw) != want {
		t.Errorf("output = %s", raw)
	}
}

func TestZeroTimesAndBoundsLeaveValuesOut(t *testing.T) {
	deep := `[{"a":{"b":{"c":{"d":1}}}}]`
	var tags strings.Builder
	for i := 0; i <= maxListItems; i++ {
		tags.WriteString("<oc:tag>t</oc:tag>")
	}
	body := `<d:response><d:href>` + aliceRoot + `/q1.pdf</d:href><d:propstat><d:prop>` +
		`<d:displayname>q1.pdf</d:displayname><d:resourcetype/><d:getcontentlength>1</d:getcontentlength>` +
		`<nc:creation_time>0</nc:creation_time><nc:upload_time>0</nc:upload_time>` +
		`<nc:share-attributes>` + deep + `</nc:share-attributes>` +
		`<ocm:share-permissions>{"x":1}</ocm:share-permissions>` +
		`<oc:tags>` + tags.String() + `</oc:tags>` +
		`<oc:comments-href>/a` + "\t" + `b</oc:comments-href>` +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus,
			multistatus(body)), nil
	})
	c, _ := client(t)
	got, err := c.StatFile(context.Background(), "q1.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if got.CreationTime != 0 || got.UploadTime != 0 || got.ShareAttributes != nil || got.OCMSharePermissions != nil ||
		got.Tags != nil || got.CommentsHref != "/ab" {
		t.Errorf("entry = %+v", got)
	}
	if !reflect.DeepEqual(got.OmittedFields, []string{fieldOCMPerms, fieldShareAttr, fieldTags}) &&
		len(got.OmittedFields) != 3 {
		t.Errorf("omitted = %v", got.OmittedFields)
	}
}

// assertEntryFieldsDeclared checks that every field an entry carries is in the output schema and the
// field list of the tool.
func assertEntryFieldsDeclared(t *testing.T, d capability.Descriptor, entry Entry, nested bool) {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(d.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	props := schema["properties"].(map[string]any)
	if nested {
		props = props["entries"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	}
	declared := map[string]bool{}
	for _, f := range d.Fields {
		declared[f.Name] = true
	}
	raw, _ := json.Marshal(entry)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	for name := range out {
		if props[name] == nil {
			t.Errorf("%s: field %s is not in the output schema", d.ID, name)
		}
		if !declared[name] {
			t.Errorf("%s: field %s is not in Fields", d.ID, name)
		}
	}
}

func TestEveryEntryFieldIsDeclaredForTheFourFileTools(t *testing.T) {
	full := wantFullEntry(true)
	full.LastActivity = "1"
	full.OmittedFields = []string{"x"}
	assertEntryFieldsDeclared(t, filesStat, full, false)
	assertEntryFieldsDeclared(t, filesList, full, true)
	assertEntryFieldsDeclared(t, filesSearch, full, true)
	assertEntryFieldsDeclared(t, favoritesList, full, true)
}

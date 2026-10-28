package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

type handlerFunc = func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error)

// excludingConnection is the reports connection (Files folder Reports) with folder exclusions, which are
// written relative to the Files root.
func excludingConnection(excludes ...string) *config.Resolved {
	r := reportsConnection()
	for _, e := range excludes {
		r.Exclude = append(r.Exclude, "folder/Reports/"+e)
	}
	return r
}

// callTool runs a handler and reports whether it read a secret.
func callTool(h handlerFunc, r *config.Resolved, args string) (result any, touched bool, err error) {
	red := &redact.Redactor{}
	res := secret.NewWith(func(name string) string {
		touched = true
		return map[string]string{aliceUserEnv: aliceUser, aliceTokenEnv: aliceToken}[name]
	}, nil, nil, red)
	result, err = h(capability.WithConfirmed(context.Background()), r, res, red, json.RawMessage(args))
	return
}

func providerFailure(t *testing.T, err error) *provider.Error {
	t.Helper()
	var perr *provider.Error
	if !errors.As(err, &perr) {
		t.Fatalf("err = %v, want a provider error", err)
	}
	return perr
}

func wantMissing(t *testing.T, err error) {
	t.Helper()
	if perr := providerFailure(t, err); perr.Message != messageNotFound || perr.Class != provider.ClassProviderError {
		t.Errorf("err = %+v, want the answer for a missing path", perr)
	}
}

func wantBlocked(t *testing.T, err error) {
	t.Helper()
	if perr := providerFailure(t, err); perr.Class != provider.ClassPermission {
		t.Errorf("err = %+v, want class permission", perr)
	}
}

func TestExcludedFolderToolsAnswerAsMissingBeforeSecretsAndRequests(t *testing.T) {
	tools := []struct {
		name string
		h    handlerFunc
		args string // %s is the path
	}{
		{"list", invokeFilesList, `{"path":"%s"}`},
		{"stat", invokeFilesStat, `{"path":"%s"}`},
		{"get", invokeFilesGet, `{"path":"%s"}`},
		{"update", invokeFilesUpdate, `{"path":"%s","content_base64":"eA==","etag":"abc"}`},
		{"update local", invokeFilesUpdate, `{"path":"%s","local_path":"/nonexistent/x","etag":"abc"}`},
		{"get local", invokeFilesGet, `{"path":"%s","local_path":"/nonexistent/x"}`},
		{"delete", invokeFilesDelete, `{"path":"%s","etag":"abc"}`},
		{"folder delete", invokeFoldersDelete, `{"path":"%s","etag":"abc"}`},
		{"versions list", invokeVersionsList, `{"path":"%s"}`},
		{"versions get", invokeVersionsGet, `{"path":"%s","version_id":"1700000000"}`},
		{"versions restore", invokeVersionsRestore, `{"path":"%s","version_id":"1700000000","etag":"abc"}`},
		{"tags list", invokeFilesTagsList, `{"path":"%s"}`},
		{"tags add", invokeFilesTagsAdd, `{"path":"%s","tag_id":"1"}`},
		{"tags remove", invokeFilesTagsRemove, `{"path":"%s","tag_id":"1"}`},
		{"comments list", invokeCommentsList, `{"path":"%s"}`},
		{"comments create", invokeCommentsCreate, `{"path":"%s","message":"hi"}`},
		{"comments update", invokeCommentsUpdate, `{"path":"%s","comment_id":"1","message":"hi"}`},
		{"comments delete", invokeCommentsDelete, `{"path":"%s","comment_id":"1"}`},
		{"favorite", invokeFilesFavorite, `{"path":"%s","favorite":true}`},
	}
	paths := []string{"Team/Private", "Team/Private/a.txt", "Team/Private/x/y/z", "team/PRIVATE/a.txt",
		"Team/Pri\u0308vate", "TEAM/private/STRASSE", "Team/U\u0308/a"}
	for _, tool := range tools {
		for _, path := range paths {
			t.Run(tool.name+" "+path, func(t *testing.T) {
				refuse(t)
				r := excludingConnection("Team/Private", "Team/Ü", "Team/Straße", "Team/Pri\u0308vate")
				_, touched, err := callTool(tool.h, r, strings.ReplaceAll(tool.args, "%s", path))
				wantMissing(t, err)
				if touched {
					t.Error("a secret was read")
				}
			})
		}
	}
}

func TestExcludedFolderNFDSpellingIsExcluded(t *testing.T) {
	refuse(t)
	r := excludingConnection("Team/Ü")
	_, touched, err := callTool(invokeFilesStat, r, `{"path":"Team/Ü/x"}`)
	wantMissing(t, err)
	if touched {
		t.Error("a secret was read")
	}
}

func TestSiblingOfAnExcludedFolderStaysReachable(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/Team/Privates/a.txt", "a.txt", "7", "1"))), nil
	})
	if _, _, err := callTool(invokeFilesStat, excludingConnection("Team/Private"), `{"path":"Team/Privates/a.txt"}`); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Errorf("calls = %d", len(*calls))
	}
}

func TestCreateBelowAnExclusionAnswersLikeAMissingParent(t *testing.T) {
	// What Nextcloud answers for a create below a folder that does not exist.
	serve(t, func(*http.Request) (*http.Response, error) { return status(409), nil })
	_, _, want := callTool(invokeFilesCreate, reportsConnection(), `{"path":"Team/nowhere/a.txt","content_base64":"eA=="}`)
	if want == nil {
		t.Fatal("the control answered without an error")
	}
	for _, c := range []struct {
		name string
		h    handlerFunc
		args string
	}{
		{"create", invokeFilesCreate, `{"path":"Team/Private/a.txt","content_base64":"eA=="}`},
		{"create deep", invokeFilesCreate, `{"path":"Team/Private/x/y/a.txt","content_base64":"eA=="}`},
		{"create local", invokeFilesCreate, `{"path":"Team/Private/a.txt","local_path":"/nonexistent/x"}`},
		{"folder", invokeFoldersCreate, `{"path":"Team/Private/new"}`},
		{"folder case", invokeFoldersCreate, `{"path":"team/private/new"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			refuse(t)
			_, touched, err := callTool(c.h, excludingConnection("Team/Private"), c.args)
			if err == nil || providerFailure(t, err).Message != providerFailure(t, want).Message || classOf(err) != classOf(want) {
				t.Errorf("err = %v, want %v", err, want)
			}
			if touched {
				t.Error("a secret was read")
			}
		})
	}
}

func TestCreatingTheExcludedFolderItselfIsRefused(t *testing.T) {
	for _, tool := range []struct {
		h    handlerFunc
		args string
	}{
		{invokeFoldersCreate, `{"path":"Team/Private"}`},
		{invokeFoldersCreate, `{"path":"TEAM/private"}`},
		{invokeFilesCreate, `{"path":"Team/Private","content_base64":"eA=="}`},
	} {
		refuse(t)
		_, touched, err := callTool(tool.h, excludingConnection("Team/Private"), tool.args)
		wantBlocked(t, err)
		if touched {
			t.Error("a secret was read")
		}
	}
}

func TestExclusionErrorsNameNeitherPathNorExclusion(t *testing.T) {
	refuse(t)
	r := excludingConnection("Team/Private")
	for _, args := range []string{`{"path":"Team/Private/a.txt"}`, `{"path":"Team/Private"}`} {
		_, _, err := callTool(invokeFilesStat, r, args)
		if text := err.Error(); strings.Contains(text, "Private") || strings.Contains(text, "a.txt") {
			t.Errorf("err = %q", text)
		}
	}
	for _, h := range []handlerFunc{invokeFoldersCreate} {
		_, _, err := callTool(h, r, `{"path":"Team/Private"}`)
		if strings.Contains(err.Error(), "Private") {
			t.Errorf("err = %q", err)
		}
	}
}

func TestListingOmitsExcludedChildren(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(
			folderXML(aliceRoot+"/Team/", "Team", "1", "9"),
			folderXML(aliceRoot+"/Team/Private/", "Private", "2", "5"),
			folderXML(aliceRoot+"/Team/PRIVATE/", "PRIVATE", "3", "5"),
			folderXML(aliceRoot+"/Team/Pri%CC%88vate/", "x", "4", "5"),
			folderXML(aliceRoot+"/Team/Open/", "Open", "5", "2"),
			fileXML(aliceRoot+"/Team/a.txt", "a.txt", "6", "1"),
		)), nil
	})
	r := excludingConnection("Team/Private", "Team/Privät")
	r.Exclude[1] = "folder/Reports/Team/Prïvate"
	result, _, err := callTool(invokeFilesList, r, `{"path":"Team"}`)
	if err != nil {
		t.Fatal(err)
	}
	list := result.(*ListResult)
	if list.Count != 2 || len(list.Entries) != 2 || list.Entries[0].Name != "Open" && list.Entries[1].Name != "Open" {
		t.Errorf("entries = %+v", list.Entries)
	}
	for _, e := range list.Entries {
		if strings.Contains(strings.ToLower(e.Path), "priv") {
			t.Errorf("an excluded entry was listed: %+v", e)
		}
	}
}

func TestSearchAndFavoritesOmitExcludedAndDoNotCountThem(t *testing.T) {
	answer := multistatus(
		fileXML(aliceRoot+"/Team/Private/a.txt", "a.txt", "1", "1"),
		fileXML(aliceRoot+"/team/private/b.txt", "b.txt", "2", "1"),
		fileXML(aliceRoot+"/Team/Open/c.txt", "c.txt", "3", "1"),
		fileXML(aliceRoot+"/Team/d.txt", "d.txt", "4", "1"),
	)
	serve(t, func(*http.Request) (*http.Response, error) { return xmlResponse(http.StatusMultiStatus, answer), nil })
	r := excludingConnection("Team/Private")

	result, _, err := callTool(invokeFilesSearch, r, `{"name_contains":"txt","limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	found := result.(*entryListResult)
	if found.Count != 2 || len(found.Entries) != 2 || found.Entries[0].Path != "Team/Open/c.txt" && found.Entries[1].Path != "Team/Open/c.txt" {
		t.Errorf("search entries = %+v", found.Entries)
	}
	// The server filled the limit, which stays the statement about truncation; excluded entries did not
	// use up the limit.
	if !found.Truncated {
		t.Errorf("truncated = %v", found.Truncated)
	}

	result, _, err = callTool(invokeFavoritesList, r, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	favorites := result.(*entryListResult)
	if favorites.Count != 2 {
		t.Errorf("favorites = %+v", favorites.Entries)
	}
	for _, e := range favorites.Entries {
		if strings.Contains(strings.ToLower(e.Path), "private") {
			t.Errorf("an excluded favorite was listed: %+v", e)
		}
	}
}

func TestTrashHidesExcludedAndRefusesAncestors(t *testing.T) {
	entries := []string{
		trashXML("a.d1", "a", "Reports/Team/Secret/a.txt", false, 1),
		trashXML("b.d2", "b", "Reports/team/SECRET", true, 0),
		trashXML("Team.d3", "Team", "Reports/Team", true, 0),
		trashXML("c.d4", "c", "Reports/Team/c.txt", false, 1),
	}
	r := excludingConnection("Team/Secret")

	calls := serve(t, func(req *http.Request) (*http.Response, error) {
		// A read of one trash item answers with that item only.
		for i, id := range []string{"a.d1", "b.d2", "Team.d3", "c.d4"} {
			if strings.HasSuffix(req.URL.Path, "/trash/"+id) {
				return trashHandler(entries[i])(req)
			}
		}
		return trashHandler(entries...)(req)
	})
	result, _, err := callTool(invokeTrashList, r, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	list := result.(*trashListResult)
	if list.Count != 2 || list.Entries[0].ID != "Team.d3" && list.Entries[1].ID != "Team.d3" {
		t.Fatalf("entries = %+v", list.Entries)
	}

	for _, tool := range []handlerFunc{invokeTrashRestore, invokeTrashDelete} {
		*calls = nil
		_, _, err := callTool(tool, r, `{"trash_id":"a.d1"}`)
		if perr := providerFailure(t, err); perr.Message != messageTrashNotFound {
			t.Errorf("hidden entry: err = %+v", perr)
		}
		_, _, err = callTool(tool, r, `{"trash_id":"b.d2"}`)
		if perr := providerFailure(t, err); perr.Message != messageTrashNotFound {
			t.Errorf("hidden folder entry: err = %+v", perr)
		}
		_, _, err = callTool(tool, r, `{"trash_id":"Team.d3"}`)
		wantBlocked(t, err)
		for _, c := range *calls {
			if c.method != methodPropfind {
				t.Errorf("a %s was sent", c.method)
			}
		}
		_, _, err = callTool(tool, r, `{"trash_id":"c.d4"}`)
		if err != nil {
			t.Errorf("a reachable entry: err = %v", err)
		}
	}
}

func TestAncestorsOfAnExclusionCannotBeDeletedMovedOrCopied(t *testing.T) {
	r := func() *config.Resolved { return excludingConnection("Team/Secret") }
	for _, c := range []struct {
		name string
		h    handlerFunc
		args string
		want func(*testing.T, error)
	}{
		{"delete folder", invokeFoldersDelete, `{"path":"Team","etag":"abc"}`, wantBlocked},
		{"delete folder case", invokeFoldersDelete, `{"path":"TEAM","etag":"abc"}`, wantBlocked},
		{"move source", invokeFilesMove, `{"path":"Team","destination":"Other"}`, wantBlocked},
		{"copy source", invokeFilesCopy, `{"path":"Team","destination":"Other"}`, wantBlocked},
		{"move destination on exclusion", invokeFilesMove, `{"path":"a.txt","destination":"Team/Secret"}`, wantBlocked},
		{"copy destination on exclusion", invokeFilesCopy, `{"path":"a.txt","destination":"team/secret"}`, wantBlocked},
		{"move destination above exclusion", invokeFilesMove, `{"path":"a.txt","destination":"Team"}`, wantBlocked},
		{"copy destination above exclusion", invokeFilesCopy, `{"path":"a.txt","destination":"Team"}`, wantBlocked},
		{"move hidden source", invokeFilesMove, `{"path":"Team/Secret/a.txt","destination":"b.txt"}`, wantMissing},
		{"copy hidden source", invokeFilesCopy, `{"path":"Team/Secret","destination":"b.txt"}`, wantMissing},
		{"move hidden source and blocked destination", invokeFilesMove, `{"path":"Team/Secret/a","destination":"Team"}`, wantMissing},
	} {
		t.Run(c.name, func(t *testing.T) {
			refuse(t)
			_, touched, err := callTool(c.h, r(), c.args)
			c.want(t, err)
			if touched {
				t.Error("a secret was read")
			}
		})
	}

	// A destination below an exclusion answers like a missing parent.
	serve(t, func(*http.Request) (*http.Response, error) { return status(409), nil })
	_, _, want := callTool(invokeFilesMove, reportsConnection(), `{"path":"a.txt","destination":"nowhere/b.txt"}`)
	for _, h := range []handlerFunc{invokeFilesMove, invokeFilesCopy} {
		refuse(t)
		_, _, err := callTool(h, r(), `{"path":"a.txt","destination":"Team/Secret/b.txt"}`)
		if err == nil || providerFailure(t, err).Message != providerFailure(t, want).Message || classOf(err) != classOf(want) {
			t.Errorf("err = %v, want %v", err, want)
		}
	}
}

func TestTransferBesideAnExclusionStillWorks(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return status(201), nil })
	r := excludingConnection("Team/Secret")
	for _, args := range []string{`{"path":"Team/Secrets","destination":"Other"}`, `{"path":"a.txt","destination":"Team/b.txt"}`} {
		if _, _, err := callTool(invokeFilesMove, r, args); err != nil {
			t.Errorf("%s: %v", args, err)
		}
	}
	if len(*calls) != 2 {
		t.Errorf("calls = %d", len(*calls))
	}
}

func TestChunkedUploadIsGatedBeforeAnyRequest(t *testing.T) {
	refuse(t)
	c, _ := client(t)
	c.scope = mustScope(t, excludingConnection("Team/Private"))
	if _, err := c.uploadChunked(context.Background(), "create file", []string{"Team", "Private", "a.bin"}, nil, true, ""); err == nil {
		t.Fatal("accepted")
	} else if perr := providerFailure(t, err); perr.Message != statusError("x", http.StatusConflict).(*provider.Error).Message {
		t.Errorf("err = %+v", perr)
	}
	if _, err := c.uploadChunked(context.Background(), "update file", []string{"Team", "Private", "a.bin"}, nil, false, "e"); err == nil {
		t.Fatal("accepted")
	} else {
		wantMissing(t, err)
	}
}

func mustScope(t *testing.T, r *config.Resolved) scope {
	t.Helper()
	s, err := scopeOf(r)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The URL check stands behind the gates: whatever builds a request below an excluded folder, it is not sent.
func TestRequestsBelowAnExclusionAreNeverSent(t *testing.T) {
	refuse(t)
	c, _ := client(t)
	c.scope = mustScope(t, excludingConnection("Team/Private"))
	ctx := context.Background()
	hidden := mainInstance + aliceRoot + "/Team/Private/a.txt"

	if _, err := c.propfindAt(ctx, "op", hidden, propfindBody, depthSelf, false, 1); err == nil {
		t.Error("propfind sent")
	} else {
		wantMissing(t, err)
	}
	if _, err := c.webdavTo(ctx, "op", http.MethodDelete, hidden, nil, "", "", "", nil); err == nil {
		t.Error("delete sent")
	}
	if _, err := c.webdavTo(ctx, "op", methodMove, mainInstance+aliceRoot+"/a.txt", nil, "", "", "",
		http.Header{"Destination": {mainInstance + aliceRoot + "/team/PRIVATE/b.txt"}}); err == nil {
		t.Error("move to a hidden destination sent")
	}
	if _, _, err := c.readInlineDAV(ctx, "op", hidden, "too large"); err == nil {
		t.Error("get sent")
	}
	if _, err := c.putStream(ctx, "op", []string{"Team", "Private", "a.txt"}, nil, true, ""); err == nil {
		t.Error("put sent")
	}
}

// The offer is derived from the targets alone; an exclusion adds nothing to the scope beyond its own field.
func TestExclusionsDoNotChangeTheToolOffer(t *testing.T) {
	targets := []string{"folder/Reports"}
	plain, err := parseScope(targets)
	if err != nil {
		t.Fatal(err)
	}
	excluding, err := parseScopeExcluding(targets, []string{"folder/Reports/Team/Private"})
	if err != nil {
		t.Fatal(err)
	}
	excluding.excludes = exclusions{}
	if !reflect.DeepEqual(plain, excluding) {
		t.Errorf("an exclusion changed the scope beyond its own field: %+v %+v", plain, excluding)
	}
}

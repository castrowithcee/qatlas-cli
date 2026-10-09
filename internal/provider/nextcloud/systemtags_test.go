package nextcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	tagCatalog   = "/remote.php/dav/systemtags"
	tagRelations = "/remote.php/dav/systemtags-relations/files/1003"
)

func tagXML(href, id, name, visible, assignable, canAssign string) string {
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:resourcetype/>` +
		`<oc:id>` + id + `</oc:id><oc:display-name>` + name + `</oc:display-name>` +
		`<oc:user-visible>` + visible + `</oc:user-visible><oc:user-assignable>` + assignable + `</oc:user-assignable>` +
		`<oc:can-assign>` + canAssign + `</oc:can-assign></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

func tagCollectionXML(href string) string {
	return `<d:response><d:href>` + href + `</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop>` +
		`<d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

// tagServer answers the stat of note.txt, the catalog, one tag, and the relations of the file; the PUT and
// DELETE of a relation are recorded and answered with change.
func tagServer(t *testing.T, tag string, change func() (*http.Response, error)) (*[]call, *[]string) {
	t.Helper()
	var changes []string
	calls := serve(t, func(request *http.Request) (*http.Response, error) {
		path := request.URL.Path
		switch {
		case request.Method == methodPropfind && strings.HasPrefix(path, "/remote.php/dav/files/"):
			return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
		case request.Method == methodPropfind && path == tagCatalog:
			return xmlResponse(http.StatusMultiStatus, multistatus(tagCollectionXML(tagCatalog+"/"),
				tagXML(tagCatalog+"/7", "7", "Invoice", "true", "true", "true"),
				tagXML(tagCatalog+"/8", "8", "Hidden", "false", "true", "true"),
				tagXML(tagCatalog+"/9", "9", "Locked", "true", "false", "false"))), nil
		case request.Method == methodPropfind && path == tagCatalog+"/7":
			return xmlResponse(http.StatusMultiStatus, multistatus(tag)), nil
		case request.Method == methodPropfind && path == tagRelations:
			return xmlResponse(http.StatusMultiStatus, multistatus(tagCollectionXML(tagRelations+"/"),
				tagXML(tagRelations+"/7", "7", "Invoice", "true", "true", "true"),
				tagXML(tagRelations+"/8", "8", "Hidden", "false", "true", "true"))), nil
		case request.Method == http.MethodPut || request.Method == http.MethodDelete:
			changes = append(changes, request.Method+" "+path)
			return change()
		}
		t.Errorf("unexpected request %s %s", request.Method, path)
		return status(500), nil
	})
	return calls, &changes
}

var okTag = tagXML(tagCatalog+"/7", "7", "Invoice", "true", "true", "true")

func okChange() (*http.Response, error) { return status(201), nil }

func invokeTag(t *testing.T, fn func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error), args string) (any, error) {
	t.Helper()
	red := &redact.Redactor{}
	return fn(capability.WithConfirmed(context.Background()), localConnection("", ""), resolver(red), red, json.RawMessage(args))
}

func accountConnection() *config.Resolved {
	resolved := resolvedConnection("reports", "cloud-reader", aliceUserEnv, aliceTokenEnv, mainInstance, "")
	resolved.Targets = []string{"account"}
	return resolved
}

func TestSystemTagsListShowsOnlyVisibleTagsFromTheFixedCatalog(t *testing.T) {
	calls, _ := tagServer(t, okTag, okChange)
	red := &redact.Redactor{}
	got, err := accountBound(invokeSystemTagsList)(context.Background(), accountConnection(), resolver(red), red, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*SystemTagList)
	if list.Count != 2 || list.Truncated || list.Tags[0] != (SystemTag{"7", "Invoice", true}) || list.Tags[1] != (SystemTag{"9", "Locked", false}) {
		t.Errorf("list = %+v", list)
	}
	if len(*calls) != 1 || (*calls)[0].method != methodPropfind || (*calls)[0].depth != depthChildren || (*calls)[0].url.Path != tagCatalog ||
		!strings.Contains((*calls)[0].body, "<oc:can-assign/>") {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestSystemTagsListIsCappedAndReportsTheCut(t *testing.T) {
	entries := []string{tagCollectionXML(tagCatalog + "/")}
	for i := 0; i < maxTags+3; i++ {
		id := fmt.Sprint(100 + i)
		entries = append(entries, tagXML(tagCatalog+"/"+id, id, "t"+id, "true", "true", "true"))
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(entries...)), nil
	})
	c, _ := client(t)
	list, err := c.ListSystemTags(context.Background())
	if err != nil || list.Count != maxTags || !list.Truncated {
		t.Errorf("list = %+v, %v", list, err)
	}
}

func TestSystemTagsListRefusesWithoutAccountTargetBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	reads := 0
	red := &redact.Redactor{}
	secrets := secret.NewWith(func(string) string { reads++; return "x" }, nil, nil, red)
	_, err := accountBound(invokeSystemTagsList)(context.Background(), localConnection("", ""), secrets, red, json.RawMessage(`{}`))
	if err == nil || reads != 0 {
		t.Errorf("err = %v, secret reads = %d", err, reads)
	}
}

func TestFileTagToolsRefuseAnAccountOnlyConnectionBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	reads := 0
	red := &redact.Redactor{}
	secrets := secret.NewWith(func(string) string { reads++; return "x" }, nil, nil, red)
	for name, fn := range map[string]capability.Handler{
		"list": folderBound(invokeFilesTagsList), "add": folderBound(invokeFilesTagsAdd), "remove": folderBound(invokeFilesTagsRemove),
	} {
		_, err := fn(capability.WithConfirmed(context.Background()), accountConnection(), secrets, red, json.RawMessage(`{"path":"note.txt","tag_id":"7"}`))
		if err == nil || reads != 0 {
			t.Errorf("%s: err = %v, secret reads = %d", name, err, reads)
		}
	}
}

func TestFileTagsListShowsOnlyVisibleTagsOfTheStatedFile(t *testing.T) {
	calls, _ := tagServer(t, okTag, okChange)
	got, err := invokeTag(t, invokeFilesTagsList, `{"path":"note.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*SystemTagList)
	if list.Path != "note.txt" || list.Count != 1 || list.Tags[0].TagID != "7" {
		t.Errorf("list = %+v", list)
	}
	if len(*calls) != 2 || (*calls)[1].url.Path != tagRelations || (*calls)[1].depth != depthChildren {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestFileTagsAddReadsThenSendsExactlyOnePut(t *testing.T) {
	calls, changes := tagServer(t, okTag, okChange)
	got, err := invokeTag(t, invokeFilesTagsAdd, `{"path":"note.txt","tag_id":"7"}`)
	if err != nil {
		t.Fatal(err)
	}
	if m := got.(map[string]any); m["added"] != true || m["tag_id"] != "7" || m["path"] != "note.txt" {
		t.Errorf("result = %v", m)
	}
	var seq []string
	for _, c := range *calls {
		seq = append(seq, c.method+" "+c.url.Path)
	}
	want := "PROPFIND " + aliceRoot + "/note.txt|PROPFIND " + tagCatalog + "/7|PUT " + tagRelations + "/7"
	if strings.Join(seq, "|") != want || len(*changes) != 1 {
		t.Errorf("requests = %v", seq)
	}
	if put := (*calls)[2]; put.body != "" {
		t.Errorf("PUT body = %q", put.body)
	}
}

func TestFileTagsRemoveReadsThenSendsExactlyOneDelete(t *testing.T) {
	_, changes := tagServer(t, okTag, okChange)
	got, err := invokeTag(t, invokeFilesTagsRemove, `{"path":"note.txt","tag_id":"7"}`)
	if err != nil || got.(map[string]any)["removed"] != true {
		t.Fatalf("got %v, %v", got, err)
	}
	if len(*changes) != 1 || (*changes)[0] != "DELETE "+tagRelations+"/7" {
		t.Errorf("changes = %v", *changes)
	}
}

func TestFileTagsRefuseAnUnassignableOrInvisibleTagWithoutAChange(t *testing.T) {
	for name, tag := range map[string]string{
		"invisible":       tagXML(tagCatalog+"/7", "7", "x", "false", "true", "true"),
		"not assignable":  tagXML(tagCatalog+"/7", "7", "x", "true", "false", "true"),
		"cannot assign":   tagXML(tagCatalog+"/7", "7", "x", "true", "true", "false"),
		"no can-assign":   `<d:response><d:href>` + tagCatalog + `/7</d:href><d:propstat><d:prop><oc:id>7</oc:id><oc:user-visible>true</oc:user-visible><oc:user-assignable>true</oc:user-assignable></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`,
		"id mismatch":     tagXML(tagCatalog+"/7", "8", "x", "true", "true", "true"),
		"foreign node":    tagXML(tagCatalog+"/8", "8", "x", "true", "true", "true"),
		"foreign origin":  tagXML("https://evil.example.invalid"+tagCatalog+"/7", "7", "x", "true", "true", "true"),
		"foreign area":    tagXML("/remote.php/dav/files/"+aliceUser+"/7", "7", "x", "true", "true", "true"),
		"traversal":       tagXML(tagCatalog+"/../x/7", "7", "x", "true", "true", "true"),
		"no readable tag": `<d:response><d:href>` + tagCatalog + `/7</d:href><d:propstat><d:prop><oc:id/></d:prop><d:status>HTTP/1.1 404 Not Found</d:status></d:propstat></d:response>`,
	} {
		for _, fn := range []func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error){invokeFilesTagsAdd, invokeFilesTagsRemove} {
			t.Run(name, func(t *testing.T) {
				_, changes := tagServer(t, tag, okChange)
				if _, err := invokeTag(t, fn, `{"path":"note.txt","tag_id":"7"}`); err == nil {
					t.Error("the tag was accepted")
				}
				if len(*changes) != 0 {
					t.Errorf("changes = %v", *changes)
				}
			})
		}
	}
}

func TestFileTagsRefuseBadInputBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	reads := 0
	red := &redact.Redactor{}
	secrets := secret.NewWith(func(string) string { reads++; return "x" }, nil, nil, red)
	for name, args := range map[string]string{
		"letters":       `{"path":"note.txt","tag_id":"abc"}`,
		"empty tag":     `{"path":"note.txt","tag_id":""}`,
		"path in tag":   `{"path":"note.txt","tag_id":"7/../8"}`,
		"long tag":      `{"path":"note.txt","tag_id":"123456789012345678901"}`,
		"root":          `{"path":"","tag_id":"7"}`,
		"outside":       `{"path":"../Audit/x","tag_id":"7"}`,
		"absolute":      `{"path":"/etc/x","tag_id":"7"}`,
		"file id":       `{"path":"note.txt","tag_id":"7","file_id":"1004"}`,
		"missing tag":   `{"path":"note.txt"}`,
		"not an object": `[]`,
	} {
		for _, fn := range []capability.Handler{folderBound(invokeFilesTagsAdd), folderBound(invokeFilesTagsRemove)} {
			if _, err := fn(capability.WithConfirmed(context.Background()), localConnection("", ""), secrets, red, json.RawMessage(args)); err == nil {
				t.Errorf("%s: accepted", name)
			}
		}
	}
	if _, err := folderBound(invokeFilesTagsList)(context.Background(), localConnection("", ""), secrets, red, json.RawMessage(`{"path":""}`)); err == nil {
		t.Error("list: root accepted")
	}
	if reads != 0 {
		t.Errorf("secret reads = %d", reads)
	}
}

func TestFileTagsConflictAndMissingRelationAreClear(t *testing.T) {
	tagServer(t, okTag, func() (*http.Response, error) { return status(409), nil })
	_, err := invokeTag(t, invokeFilesTagsAdd, `{"path":"note.txt","tag_id":"7"}`)
	if err == nil || !strings.Contains(err.Error(), messageTagAssigned) || strings.Contains(err.Error(), "may have been") {
		t.Errorf("409: %v", err)
	}
	tagServer(t, okTag, func() (*http.Response, error) { return status(404), nil })
	_, err = invokeTag(t, invokeFilesTagsRemove, `{"path":"note.txt","tag_id":"7"}`)
	if err == nil || !strings.Contains(err.Error(), messageTagNotOnFile) || strings.Contains(err.Error(), "may have been") {
		t.Errorf("404: %v", err)
	}
}

func TestFileTagChangesWithAnUnclearOutcomeAreNeverRepeated(t *testing.T) {
	for name, change := range map[string]func() (*http.Response, error){
		"500":     func() (*http.Response, error) { return status(500), nil },
		"502":     func() (*http.Response, error) { return status(502), nil },
		"timeout": func() (*http.Response, error) { return nil, timeoutError{} },
		"reset":   func() (*http.Response, error) { return nil, fmt.Errorf("connection reset") },
	} {
		for fn, hint := range map[string]string{"add": "may have been assigned", "remove": "may have been removed"} {
			t.Run(name+" "+fn, func(t *testing.T) {
				_, changes := tagServer(t, okTag, change)
				handler := invokeFilesTagsAdd
				if fn == "remove" {
					handler = invokeFilesTagsRemove
				}
				_, err := invokeTag(t, handler, `{"path":"note.txt","tag_id":"7"}`)
				if err == nil || !strings.Contains(err.Error(), hint) || !strings.Contains(err.Error(), "filetags.list") {
					t.Errorf("err = %v", err)
				}
				if len(*changes) != 1 {
					t.Errorf("changes = %v", *changes)
				}
			})
		}
	}
}

func TestFileTagChangesRefuseRedirectsAndClientErrorsWithoutAnUncertainHint(t *testing.T) {
	for name, change := range map[string]func() (*http.Response, error){
		"302": func() (*http.Response, error) {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {mainInstance + redirectTarget}}, Body: status(302).Body}, nil
		},
		"403": func() (*http.Response, error) { return status(403), nil },
	} {
		t.Run(name, func(t *testing.T) {
			_, changes := tagServer(t, okTag, change)
			_, err := invokeTag(t, invokeFilesTagsAdd, `{"path":"note.txt","tag_id":"7"}`)
			if err == nil || strings.Contains(err.Error(), "may have been") || strings.Contains(err.Error(), "evil") ||
				strings.Contains(err.Error(), redirectTarget) || len(*changes) != 1 {
				t.Errorf("err = %v, changes = %v", err, *changes)
			}
			if name == "302" && !strings.Contains(err.Error(), messageRedirect) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestFileTagListsRefuseForeignNodesAndRedirects(t *testing.T) {
	for name, href := range map[string]string{
		"other file":   "/remote.php/dav/systemtags-relations/files/1004/7",
		"catalog":      tagCatalog + "/7",
		"other origin": "https://evil.example.invalid" + tagRelations + "/7",
		"deeper":       tagRelations + "/7/x",
		"traversal":    tagRelations + "/../1004/7",
	} {
		t.Run(name, func(t *testing.T) {
			serve(t, func(request *http.Request) (*http.Response, error) {
				if strings.HasPrefix(request.URL.Path, "/remote.php/dav/files/") {
					return xmlResponse(http.StatusMultiStatus, multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
				}
				return xmlResponse(http.StatusMultiStatus, multistatus(tagCollectionXML(tagRelations+"/"),
					tagXML(href, "7", "x", "true", "true", "true"))), nil
			})
			if _, err := invokeTag(t, invokeFilesTagsList, `{"path":"note.txt"}`); err == nil {
				t.Error("a foreign node was accepted")
			}
		})
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://evil.example.invalid/x"}}, Body: status(302).Body}, nil
	})
	c, _ := client(t)
	if _, err := c.ListSystemTags(context.Background()); err == nil || !strings.Contains(err.Error(), messageRedirect) || strings.Contains(err.Error(), "evil") {
		t.Errorf("redirect: %v", err)
	}
}

func TestFileTagsWorkOnFoldersAndRefuseAMissingFileID(t *testing.T) {
	serve(t, func(request *http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(folderXML(aliceRoot+"/note.txt/", "note.txt", "", "0"))), nil
	})
	if _, err := invokeTag(t, invokeFilesTagsList, `{"path":"note.txt"}`); err == nil {
		t.Error("a path without file ID was accepted")
	}
}

func TestFileTagToolsRisksProfilesAndGroup(t *testing.T) {
	reg := registry(t)
	want := map[string]capability.Effect{"nextcloud.systemtags.list": capability.EffectRead, "nextcloud.filetags.list": capability.EffectRead,
		"nextcloud.filetags.add": capability.EffectUpdate, "nextcloud.filetags.remove": capability.EffectUpdate}
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
		if effect == capability.EffectUpdate && r.Confirmation != capability.ConfirmationRequired {
			t.Errorf("%s confirmation = %q", d.ID, r.Confirmation)
		}
	}
	if len(want) != 0 {
		t.Errorf("not registered: %v", want)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		if p.ID == "deck-read" || p.ID == "calendar" {
			continue
		}
		has := false
		for _, id := range p.Tools {
			has = has || id == filesTagsList.ID
			if id == filesTagsAdd.ID || id == filesTagsRemove.ID || id == systemtagsList.ID {
				t.Errorf("profile %s holds %s", p.ID, id)
			}
		}
		if !has {
			t.Errorf("profile %s lacks %s", p.ID, filesTagsList.ID)
		}
	}
}

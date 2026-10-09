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

const trashBase = "/remote.php/dav/trashbin/" + aliceUser

func trashXML(id, name, origin string, folder bool, size int) string {
	kind := ""
	if folder {
		kind = "<d:collection/>"
	}
	return `<d:response><d:href>` + trashBase + `/trash/` + id + `</d:href><d:propstat><d:prop>` +
		`<d:resourcetype>` + kind + `</d:resourcetype><d:getcontentlength>` + fmt.Sprint(size) + `</d:getcontentlength>` +
		`<nc:trashbin-filename>` + name + `</nc:trashbin-filename>` +
		`<nc:trashbin-original-location>` + origin + `</nc:trashbin-original-location>` +
		`<nc:trashbin-deletion-time>1700000000</nc:trashbin-deletion-time>` +
		`</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

const trashSelf = `<d:response><d:href>` + trashBase + `/trash/</d:href><d:propstat><d:prop>` +
	`<d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`

func trashHandler(entries ...string) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if r.Method == methodPropfind {
			if strings.HasSuffix(r.URL.Path, "/trash") {
				return xmlResponse(http.StatusMultiStatus, multistatus(append([]string{trashSelf}, entries...)...)), nil
			}
			return xmlResponse(http.StatusMultiStatus, multistatus(entries...)), nil
		}
		return status(204), nil
	}
}

func runTrash(connection *config.Resolved, handler func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error), args string) (any, error) {
	red := &redact.Redactor{}
	return handler(capability.WithConfirmed(context.Background()), connection, resolver(red), red, json.RawMessage(args))
}

func listTrash(t *testing.T, connection *config.Resolved) *trashListResult {
	t.Helper()
	result, err := runTrash(connection, invokeTrashList, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	return result.(*trashListResult)
}

func TestTrashListFiltersByOriginalLocation(t *testing.T) {
	entries := []string{
		trashXML("a.pdf.d1", "a.pdf", "Reports/2026/a.pdf", false, 12),
		trashXML("old.d2", "old", "Reports/old", true, 0),
		trashXML("other.txt.d3", "other.txt", "Private/other.txt", false, 5),
		trashXML("Reports.d4", "Reports", "Reports", true, 0),
		trashXML("root.txt.d5", "root.txt", "root.txt", false, 1),
		trashXML("ReportsX.d6", "x", "ReportsX/x", false, 1),
	}
	calls := serve(t, trashHandler(entries...))
	result := listTrash(t, reportsConnection())
	if result.Count != 2 || result.Entries[1].ID != "old.d2" || result.Entries[0].ID != "a.pdf.d1" {
		t.Fatalf("entries = %+v", result.Entries)
	}
	first := result.Entries[0]
	if first.Path != "2026/a.pdf" || first.Name != "a.pdf" || first.Type != "file" || first.Size != 12 ||
		first.DeletedAt != "2023-11-14T22:13:20Z" || result.Entries[1].Type != "folder" || result.Truncated {
		t.Errorf("entry = %+v", first)
	}
	if len(*calls) != 1 || (*calls)[0].method != methodPropfind || (*calls)[0].depth != "1" ||
		(*calls)[0].url.EscapedPath() != trashBase+"/trash" || (*calls)[0].auth != basicAuth(aliceUser, aliceToken) {
		t.Errorf("calls = %+v", *calls)
	}
	if !strings.Contains((*calls)[0].body, "trashbin-original-location") {
		t.Errorf("body = %q", (*calls)[0].body)
	}

	serve(t, trashHandler(entries...))
	whole := resolvedConnection("all", "cloud-reader", aliceUserEnv, aliceTokenEnv, mainInstance, "/")
	if all := listTrash(t, whole); all.Count != 6 {
		t.Errorf("root / = %+v", all.Entries)
	}
}

func TestTrashListTruncatesAtTheCap(t *testing.T) {
	var entries []string
	for i := 0; i < maxEntries+3; i++ {
		entries = append(entries, trashXML(fmt.Sprintf("f%04d.d1", i), "f", fmt.Sprintf("Reports/f%04d", i), false, 1))
	}
	serve(t, trashHandler(entries...))
	result := listTrash(t, reportsConnection())
	if result.Count != maxEntries || !result.Truncated {
		t.Errorf("count = %d, truncated = %v", result.Count, result.Truncated)
	}
}

func TestTrashListWithoutTheAppIsAClearError(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return status(404), nil })
	_, err := runTrash(reportsConnection(), invokeTrashList, `{}`)
	if err == nil || !strings.Contains(err.Error(), "files_trashbin") || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}
}

func TestTrashChangeReadsOnceThenSendsOneRequest(t *testing.T) {
	cases := []struct {
		name    string
		handler func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error)
		method  string
		done    string
	}{
		{"restore", invokeTrashRestore, "MOVE", "restored"},
		{"delete", invokeTrashDelete, "DELETE", "deleted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var destination string
			calls := serve(t, func(r *http.Request) (*http.Response, error) {
				if r.Method == c.method {
					destination = r.Header.Get("Destination")
				}
				return trashHandler(trashXML("a b.pdf.d1", "a b.pdf", "Reports/2026/a b.pdf", false, 12))(r)
			})
			result, err := runTrash(reportsConnection(), c.handler, `{"trash_id":"a b.pdf.d1"}`)
			if err != nil {
				t.Fatal(err)
			}
			got := result.(map[string]any)
			if got[c.done] != true || got["path"] != "2026/a b.pdf" {
				t.Errorf("result = %v", got)
			}
			if len(*calls) != 2 || (*calls)[0].method != methodPropfind || (*calls)[0].depth != "0" ||
				(*calls)[1].method != c.method || (*calls)[1].url.EscapedPath() != trashBase+"/trash/a%20b.pdf.d1" {
				t.Fatalf("calls = %+v", *calls)
			}
			if c.method == "MOVE" && destination != mainInstance+trashBase+"/restore/a%20b.pdf.d1" {
				t.Errorf("Destination = %q", destination)
			}
			if c.method == "DELETE" && destination != "" {
				t.Errorf("Destination = %q", destination)
			}
		})
	}
}

func TestTrashChangeRefusesForeignEntriesWithoutMutation(t *testing.T) {
	for name, handler := range map[string]func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error){
		"restore": invokeTrashRestore, "delete": invokeTrashDelete,
	} {
		t.Run(name, func(t *testing.T) {
			calls := serve(t, trashHandler(trashXML("x.d1", "x", "Private/secret-plan.txt", false, 1)))
			_, err := runTrash(reportsConnection(), handler, `{"trash_id":"x.d1"}`)
			if err == nil || !strings.Contains(err.Error(), "no such entry") || strings.Contains(err.Error(), "Private") ||
				strings.Contains(err.Error(), "secret-plan") {
				t.Errorf("err = %v", err)
			}
			if len(*calls) != 1 || (*calls)[0].method != methodPropfind {
				t.Errorf("calls = %+v", *calls)
			}
		})
	}
}

func TestTrashChangeRefusesAMissingEntry(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return status(404), nil })
	_, err := runTrash(reportsConnection(), invokeTrashDelete, `{"trash_id":"x.d1"}`)
	if err == nil || !strings.Contains(err.Error(), "no such entry") || len(*calls) != 1 {
		t.Errorf("err = %v, calls = %+v", err, *calls)
	}
}

func TestTrashChangeUnclearOutcomeIsReportedAndNotRepeated(t *testing.T) {
	cases := []struct {
		name, method, hint string
		handler            func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error)
	}{
		{"restore", "MOVE", "may have been restored", invokeTrashRestore},
		{"delete", "DELETE", "deleted for good", invokeTrashDelete},
	}
	answers := map[string]func() (*http.Response, error){
		"500":     func() (*http.Response, error) { return status(500), nil },
		"timeout": func() (*http.Response, error) { return nil, timeoutError{} },
	}
	for _, c := range cases {
		for name, answer := range answers {
			t.Run(c.name+" "+name, func(t *testing.T) {
				calls := serve(t, func(r *http.Request) (*http.Response, error) {
					if r.Method == c.method {
						return answer()
					}
					return trashHandler(trashXML("x.d1", "x", "Reports/x", false, 1))(r)
				})
				_, err := runTrash(reportsConnection(), c.handler, `{"trash_id":"x.d1"}`)
				if err == nil || !strings.Contains(err.Error(), c.hint) || strings.Contains(err.Error(), bodyCanary) {
					t.Errorf("err = %v", err)
				}
				if len(*calls) != 2 {
					t.Errorf("calls = %+v", *calls)
				}
			})
		}
		t.Run(c.name+" refusal and redirect are clear", func(t *testing.T) {
			for _, code := range []int{403, 302} {
				serve(t, func(r *http.Request) (*http.Response, error) {
					if r.Method == c.method {
						return status(code), nil
					}
					return trashHandler(trashXML("x.d1", "x", "Reports/x", false, 1))(r)
				})
				_, err := runTrash(reportsConnection(), c.handler, `{"trash_id":"x.d1"}`)
				if err == nil || strings.Contains(err.Error(), "may have been") {
					t.Errorf("status %d: err = %v", code, err)
				}
			}
		})
	}
}

func TestTrashRefusesBadInputBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	for _, id := range []string{"", ".", "..", "a/b", `a\b`, "a%2fb", "a\nb", "a\x00b", strings.Repeat("a", 300)} {
		for _, handler := range []func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error){invokeTrashRestore, invokeTrashDelete} {
			raw, _ := json.Marshal(map[string]string{"trash_id": id})
			if _, err := handler(capability.WithConfirmed(context.Background()), reportsConnection(), res, &redact.Redactor{}, raw); err == nil {
				t.Errorf("trash_id %q was accepted", id)
			}
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func TestTrashToolsNeedAFolderTargetBeforeSecrets(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	noFolder := resolvedConnection("cal", "cloud-reader", aliceUserEnv, aliceTokenEnv, mainInstance, "")
	noFolder.Targets = []string{"calendar"}
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{trashList.ID, trashRestore.ID, trashDelete.ID} {
		_, handler, ok := reg.Lookup(id)
		if !ok {
			t.Fatalf("%s is not registered", id)
		}
		if _, err := handler(capability.WithConfirmed(context.Background()), noFolder, res, &redact.Redactor{}, json.RawMessage(`{"trash_id":"x.d1"}`)); err == nil {
			t.Errorf("%s ran without a folder target", id)
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func TestTrashDescriptors(t *testing.T) {
	if trashList.Risk.Effect != capability.EffectRead || trashList.RequiresToolAllowList ||
		trashRestore.Risk.Effect != capability.EffectUpdate || trashRestore.RequiresToolAllowList ||
		trashDelete.Risk.Effect != capability.EffectDelete || !trashDelete.RequiresToolAllowList {
		t.Fatal("unexpected effects or tool list requirements")
	}
	for _, d := range []capability.Descriptor{trashRestore, trashDelete} {
		if d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity == "" || d.Risk.Idempotency == "" {
			t.Errorf("%s risk = %+v", d.ID, d.Risk)
		}
	}
}

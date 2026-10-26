package nextcloud

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	sabreExceptionName = `Sabre\DAV\Exception\NotFound`
	rfcErrorBody       = `<?xml version="1.0"?><d:error xmlns:d="DAV:">` +
		`<d:lock-token-submitted><d:href>/locked.txt</d:href></d:lock-token-submitted></d:error>`
	sabreErrorBody = `<?xml version="1.0"?><d:error xmlns:d="DAV:" xmlns:s="http://sabredav.org/ns">` +
		`<s:exception>` + sabreExceptionName + `</s:exception><s:message>` + bodyCanary + `</s:message></d:error>`
)

// davBodies are the two error documents Nextcloud answers with and what the original of each holds.
var davBodies = map[string]struct {
	body    string
	entries []provider.OriginalEntry
}{
	"rfc 4918": {rfcErrorBody, []provider.OriginalEntry{{Type: "lock-token-submitted"}}},
	"sabredav": {sabreErrorBody, []provider.OriginalEntry{{Type: sabreExceptionName, Message: bodyCanary}}},
}

func originalOf(err error) *provider.Original {
	var failure *provider.Error
	if errors.As(err, &failure) {
		return failure.Original
	}
	return nil
}

// checkOriginal proves the status and the entries of the original and that the message of Qatlas stays free
// of the provider text.
func checkOriginal(t *testing.T, err error, status int, entries []provider.OriginalEntry) {
	t.Helper()
	if err == nil {
		t.Fatal("the failing answer was accepted")
	}
	original := originalOf(err)
	if original == nil {
		t.Fatalf("the error carries no original: %v", err)
	}
	if original.Status != status || !reflect.DeepEqual(original.Entries, entries) {
		t.Errorf("original = %+v, want status %d and entries %+v", original, status, entries)
	}
	for _, canary := range []string{bodyCanary, sabreExceptionName, "lock-token-submitted"} {
		if strings.Contains(err.Error(), canary) {
			t.Errorf("the message carries %q: %v", canary, err)
		}
	}
}

// Every failing WebDAV step of the files tools, including each step of a chunked upload, passes the status and
// the DAV error document of the answer on as the original.
func TestFilesDAVErrorsCarryTheOriginal(t *testing.T) {
	for _, tt := range redirectCases() {
		if tt.name == "connection test" {
			continue
		}
		for _, status := range []int{400, 401, 403, 404, 422, 500} {
			for form, answer := range davBodies {
				t.Run(tt.name+" "+strconv.Itoa(status)+" "+form, func(t *testing.T) {
					dir := t.TempDir()
					if err := os.WriteFile(filepath.Join(dir, "in.bin"), []byte("0123456789"), 0o600); err != nil {
						t.Fatal(err)
					}
					serve(t, func(request *http.Request) (*http.Response, error) {
						body := func(s string) io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }
						if request.Method == tt.at {
							return &http.Response{StatusCode: status, Header: http.Header{}, Body: body(answer.body)}, nil
						}
						switch request.Method {
						case methodPropfind:
							if !tt.exists {
								return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: body("")}, nil
							}
							return xmlResponse(http.StatusMultiStatus,
								multistatus(fileXML(aliceRoot+"/note.txt", "note.txt", "1003", "5"))), nil
						case http.MethodGet:
							return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body("hello")}, nil
						}
						return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{}, Body: body("")}, nil
					})
					red := &redact.Redactor{}
					err := tt.run(t, localConnection(dir, dir), red, dir)
					checkOriginal(t, err, status, answer.entries)
				})
			}
		}
	}
}

// A redirect is not followed, and its body, which may name another host, never reaches the original.
func TestRedirectOriginalHoldsOnlyTheStatus(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		response := xmlResponse(http.StatusFound, sabreErrorBody)
		response.Header.Set("Location", mainInstance+redirectTarget)
		return response, nil
	})
	c, _ := client(t)
	_, err := c.ListFiles(context.Background(), "")
	checkOriginal(t, err, http.StatusFound, nil)
}

// The status line of one resource in a multi-status answer is no HTTP answer and carries no original.
func TestResourceStatusFailureCarriesNoOriginal(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, multistatus(`<d:response><d:href>`+aliceRoot+`/</d:href>`+
			`<d:status>HTTP/1.1 403 Forbidden</d:status></d:response>`)), nil
	})
	c, _ := client(t)
	_, err := c.ListFiles(context.Background(), "")
	if err == nil || classOf(err) != provider.ClassPermission {
		t.Fatalf("err = %v, want a permission error", err)
	}
	if original := originalOf(err); original != nil {
		t.Errorf("original = %+v, want none", original)
	}
}

// The Notes API is no WebDAV: its failed answer is classified as before and carries no original.
func TestNotesErrorsCarryNoDAVOriginal(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return noteResponse(http.StatusForbidden, nil, sabreErrorBody), nil
	})
	_, err := runNote(invokeNotesList, listed("notes/C"), `{}`)
	if err == nil || classOf(err) != provider.ClassPermission {
		t.Fatalf("err = %v, want a permission error", err)
	}
	if original := originalOf(err); original != nil {
		t.Errorf("original = %+v, want none", original)
	}
}

// The contacts and calendar tools pass the DAV error document of every failing step on.
func TestContactsAndCalendarErrorsCarryTheOriginal(t *testing.T) {
	failing := func(status int, body string) func() (*http.Response, error) {
		return func() (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
		}
	}
	for _, status := range []int{400, 401, 403, 404, 422, 500} {
		for form, answer := range davBodies {
			name := strconv.Itoa(status) + " " + form
			t.Run("contacts list "+name, func(t *testing.T) {
				serve(t, func(*http.Request) (*http.Response, error) { return failing(status, answer.body)() })
				_, err := bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.contacts.list",
					`{"addressbook":"contacts"}`)
				checkOriginal(t, err, status, answer.entries)
			})
			t.Run("contacts get "+name, func(t *testing.T) {
				serve(t, func(*http.Request) (*http.Response, error) { return failing(status, answer.body)() })
				_, err := bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.contacts.get",
					`{"addressbook":"contacts","id":"a.vcf"}`)
				checkOriginal(t, err, status, answer.entries)
			})
			t.Run("events get "+name, func(t *testing.T) {
				serve(t, func(*http.Request) (*http.Response, error) { return failing(status, answer.body)() })
				_, err := calInvoke(t, []string{"calendar"}, "nextcloud.calendar.events.get",
					`{"calendar":"personal","id":"a.ics"}`)
				checkOriginal(t, err, status, answer.entries)
			})
			t.Run("events list "+name, func(t *testing.T) {
				serve(t, func(*http.Request) (*http.Response, error) { return failing(status, answer.body)() })
				_, err := calInvoke(t, []string{"calendar"}, "nextcloud.calendar.events.list",
					`{"calendar":"personal","start":"2026-03-01T00:00:00Z","end":"2026-04-01T00:00:00Z"}`)
				checkOriginal(t, err, status, answer.entries)
			})
			t.Run("contacts update read "+name, func(t *testing.T) {
				serve(t, func(r *http.Request) (*http.Response, error) {
					if r.Method == methodPropfind {
						return contactBookAnswer(fullRights), nil
					}
					return failing(status, answer.body)()
				})
				_, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts.update",
					contactUpdateArgs, true)
				checkOriginal(t, err, status, answer.entries)
			})
			for op, args := range map[string]string{
				"create": contactCreateArgs, "update": contactUpdateArgs, "delete": contactDeleteArgs,
			} {
				t.Run("contacts "+op+" write "+name, func(t *testing.T) {
					_, _ = contactFixture(t, plainCard, failing(status, answer.body))
					_, err := contactWrite(t, contactTools, []string{"addressbook"}, "nextcloud.contacts.contacts."+op,
						args, true)
					// A 403 or 412 of the write has its own message, but the original is still carried only
					// where the status is classified by the shared funnel.
					if status == http.StatusForbidden {
						if err == nil {
							t.Fatal("the refused write was accepted")
						}
						return
					}
					checkOriginal(t, err, status, answer.entries)
				})
			}
			for op, args := range map[string]string{
				"create": eventCreateArgs, "update": eventUpdateArgs, "delete": eventDeleteArgs,
			} {
				t.Run("events "+op+" write "+name, func(t *testing.T) {
					_, _ = eventFixture(t, failing(status, answer.body))
					_, err := eventWrite(t, eventTools, []string{"calendar"}, "nextcloud.calendar.events."+op, args, true)
					if status == http.StatusForbidden {
						if err == nil {
							t.Fatal("the refused write was accepted")
						}
						return
					}
					checkOriginal(t, err, status, answer.entries)
				})
			}
			for op, args := range map[string]string{
				"create": `{"name":"X"}`,
				"update": `{"calendar":"projects","name":"N"}`,
				"delete": `{"calendar":"projects"}`,
			} {
				t.Run("calendars "+op+" "+name, func(t *testing.T) {
					manageFixture(t, aliceUser, failing(status, answer.body))
					_, err := manage(t, []string{"calendar"}, "nextcloud.calendar.calendars."+op, args, true)
					checkOriginal(t, err, status, answer.entries)
				})
			}
		}
	}
}

// A message rewritten for comments or Talk keeps the original of the error it replaces.
func TestRewrittenMessagesKeepTheOriginal(t *testing.T) {
	original := &provider.Original{Status: 404, Entries: []provider.OriginalEntry{{Message: "kept"}}}
	notFound := statusError("op", http.StatusNotFound).(*provider.Error)
	notFound.Original = original
	forbidden := statusError("op", http.StatusForbidden).(*provider.Error)
	forbidden.Original = original

	for name, err := range map[string]error{
		"talk not found":    talkNotFound("op", notFound, messageTalkNoRoom),
		"comment not found": commentRefusal("op", notFound, "forbidden"),
		"comment forbidden": commentRefusal("op", forbidden, "forbidden"),
	} {
		if got := originalOf(err); got != original {
			t.Errorf("%s: original = %+v, want %+v", name, got, original)
		}
	}
	if err := talkNotFound("op", notFound, messageTalkNoRoom); classOf(err) != provider.ClassNotFound ||
		!strings.Contains(err.Error(), messageTalkNoRoom) || strings.Contains(err.Error(), "kept") {
		t.Errorf("talk not found = %v", err)
	}
	if err := commentRefusal("op", notFound, "forbidden"); !strings.Contains(err.Error(), messageCommentMissing) ||
		strings.Contains(err.Error(), "kept") {
		t.Errorf("comment not found = %v", err)
	}
}

// A message that replaces the one of a classified WebDAV answer keeps the original of that answer.
func TestReplacedWebDAVMessagesKeepTheOriginal(t *testing.T) {
	answer := davBodies["sabredav"]
	reply := func(status int) func() (*http.Response, error) {
		return func() (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{},
				Body: io.NopCloser(strings.NewReader(answer.body))}, nil
		}
	}

	t.Run("event collation", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) { return reply(http.StatusForbidden)() })
		_, err := listEvents(t, `{"calendar":"personal",`+filterRange+
			`,"property_filters":[{"property":"SUMMARY","text":"a","collation":"i;octet"}]}`)
		if err == nil || !strings.Contains(err.Error(), "rejected the filter") {
			t.Fatalf("err = %v", err)
		}
		checkOriginal(t, err, http.StatusForbidden, answer.entries)
	})
	t.Run("contact collation", func(t *testing.T) {
		serve(t, func(*http.Request) (*http.Response, error) { return reply(http.StatusForbidden)() })
		_, err := listContacts(t,
			`{"addressbook":"contacts","property_filters":[{"property":"FN","text":"a","collation":"i;octet"}]}`)
		if err == nil || !strings.Contains(err.Error(), "rejected the filter") {
			t.Fatalf("err = %v", err)
		}
		checkOriginal(t, err, http.StatusForbidden, answer.entries)
	})
	t.Run("tag already assigned", func(t *testing.T) {
		tagServer(t, okTag, reply(http.StatusConflict))
		_, err := invokeTag(t, invokeFilesTagsAdd, `{"path":"note.txt","tag_id":"7"}`)
		if err == nil || !strings.Contains(err.Error(), messageTagAssigned) {
			t.Fatalf("err = %v", err)
		}
		checkOriginal(t, err, http.StatusConflict, answer.entries)
	})
	t.Run("tag not on the file", func(t *testing.T) {
		tagServer(t, okTag, reply(http.StatusNotFound))
		_, err := invokeTag(t, invokeFilesTagsRemove, `{"path":"note.txt","tag_id":"7"}`)
		if err == nil || !strings.Contains(err.Error(), messageTagNotOnFile) {
			t.Fatalf("err = %v", err)
		}
		checkOriginal(t, err, http.StatusNotFound, answer.entries)
	})
	for _, code := range []int{http.StatusConflict, http.StatusPreconditionFailed} {
		t.Run("calendar trash restore "+strconv.Itoa(code), func(t *testing.T) {
			entryFixture(t, "projects", reply(code))
			_, err := calendarTrash(t, []string{"calendar"}, "nextcloud.calendar.trash.restore",
				`{"id":"a.ics-d1"}`, true)
			if err == nil || !strings.Contains(err.Error(), "nothing was changed") {
				t.Fatalf("err = %v", err)
			}
			checkOriginal(t, err, code, answer.entries)
		})
	}
}

package infomaniakdrive

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const dropboxURL = "https://kdrive.infomaniak.com/app/dropbox/5001/0b1c2d3e-aaaa-bbbb-cccc-1234567890ab"

func dropboxJSONOf(rawURL string) string {
	return fmt.Sprintf(`{"id":12,"uuid":"0b1c2d3e","name":"Invoices","url":%q,"users_count":3,"created_by":7,`+
		`"created_at":1735689600,"updated_at":1735776000,"last_uploaded_at":null,"capabilities":{"has_password":true,`+
		`"has_notification":false,"has_validity":true,"has_size_limit":false}}`, rawURL)
}

func dropboxArgs(extra string) string {
	return fmt.Sprintf(`{"drive_id":%d,"file_id":%d%s}`, ownDrive, childFileID, extra)
}

func dropboxPathOf(fileID int64) string {
	return fmt.Sprintf("/2/drive/%d/files/%d/dropbox", ownDrive, fileID)
}

func dropboxCases() []linkCase {
	return []linkCase{
		{"delete", dropboxDelete.ID, dropboxArgs(""), http.MethodDelete, "", envelopeSuccess(`true`)},
	}
}

func TestDropboxDescriptors(t *testing.T) {
	want := map[string]struct {
		effect  capability.Effect
		confirm bool
	}{dropboxGet.ID: {capability.EffectRead, false}, dropboxDelete.ID: {capability.EffectDelete, true}}
	for _, d := range []capability.Descriptor{dropboxGet, dropboxDelete} {
		w := want[d.ID]
		r := d.Risk
		if r.Effect != w.effect || !d.RequiresToolAllowList || !r.OpenWorld || r.DataSensitivity != dropboxSensitivity {
			t.Errorf("%s = %+v list=%v", d.ID, r, d.RequiresToolAllowList)
		}
		if w.confirm && r.Confirmation != capability.ConfirmationRequired || !w.confirm && r.Confirmation != capability.ConfirmationNone {
			t.Errorf("%s confirmation = %s", d.ID, r.Confirmation)
		}
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if strings.HasPrefix(id, Provider+".dropbox.") {
				t.Errorf("profile %s contains %s", profile.ID, id)
			}
		}
	}
}

func TestDropboxChangesSendExactlyOneRequest(t *testing.T) {
	for _, tt := range dropboxCases() {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, tt.success), nil
			}), nil)
			result, err := env.invokeConfirmed(tt.tool, "dropbox", tt.args)
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			if len(calls) != 2 || calls[0].path != ownershipPath(ownDrive) {
				t.Fatalf("calls = %+v, want the ownership check then one request", calls)
			}
			got := calls[1]
			if got.method != tt.method || got.path != dropboxPathOf(childFileID) || len(got.query) != 0 {
				t.Fatalf("call = %+v, want %s %s", got, tt.method, dropboxPathOf(childFileID))
			}
			if tt.body != "" && !jsonEqual(got.body, tt.body) || tt.body == "" && got.body != "" {
				t.Fatalf("body = %q, want %q", got.body, tt.body)
			}
			var out DropboxResult
			if err := json.Unmarshal([]byte(result), &out); err != nil || out.Status != statusDone ||
				out.DriveID != ownDrive || out.FileID != childFileID {
				t.Fatalf("result = %s, %v", result, err)
			}
			if out.Dropbox != nil {
				t.Fatalf("result = %s, want no dropbox", result)
			}
		})
	}
}

func TestDropboxToolsNeedConfirmationAndToolList(t *testing.T) {
	for _, tt := range dropboxCases() {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			_, err := env.invoke(tt.tool, "dropbox", tt.args)
			var needed *application.ConfirmationRequiredError
			if !errors.As(err, &needed) {
				t.Fatalf("err = %v, want confirmation required", err)
			}
			for _, connection := range []string{"dropboxunlisted", "drive", "readonly", "links"} {
				if _, err := env.invokeConfirmed(tt.tool, connection, tt.args); err == nil {
					t.Fatalf("connection %s ran %s", connection, tt.name)
				}
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
		})
	}
	var calls []call
	env := newEnvironment(t, &calls, func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected request")
		return nil, nil
	}, nil)
	if _, err := env.invoke(dropboxGet.ID, "dropboxunlisted", dropboxArgs("")); err == nil || len(calls) != 0 {
		t.Fatalf("get ran without a tools list: %v", err)
	}
}

func TestDropboxToolsRefuseForeignDrives(t *testing.T) {
	cases := append(dropboxCases(), linkCase{name: "get", tool: dropboxGet.ID, args: dropboxArgs("")})
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			foreign := strings.Replace(tt.args, fmt.Sprintf(`"drive_id":%d`, ownDrive),
				fmt.Sprintf(`"drive_id":%d`, foreignDrive), 1)
			_, err := env.invokeConfirmed(tt.tool, "dropbox", foreign)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), fmt.Sprint(foreignDrive)) {
				t.Fatalf("err = %v, want an invalid request without the foreign value", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
			calls = nil
			env = newEnvironment(t, &calls, withOwnership(foreignDrive, otherAccount, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("request %s reached a drive of another account", r.URL.Path)
				return nil, nil
			}), nil)
			_, err = env.invokeConfirmed(tt.tool, "dropboxforeign", foreign)
			if !isInvalidRequest(err) || len(calls) != 1 || calls[0].method != http.MethodGet {
				t.Fatalf("err = %v, calls = %+v, want only the ownership check and a refusal", err, calls)
			}
		})
	}
}

func TestDropboxToolsValidateArguments(t *testing.T) {
	cases := []struct{ name, tool, args string }{
		{"root get", dropboxGet.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1}`, ownDrive)},
		{"root delete", dropboxDelete.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1}`, ownDrive)},
		{"zero id", dropboxDelete.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":0}`, ownDrive)},
		{"string id", dropboxGet.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":"5/../9"}`, ownDrive)},
		{"extra on get", dropboxGet.ID, dropboxArgs(`,"alias":"x"`)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			if _, err := env.invokeConfirmed(tt.tool, "dropbox", tt.args); err == nil {
				t.Fatal("a malformed request was accepted")
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

func TestDropboxErrorsAndUncertainOutcomesAreNeverRepeated(t *testing.T) {
	statuses := []struct {
		status int
		class  provider.Class
		want   string
	}{
		{403, provider.ClassPermission, "plan"},
		{404, provider.ClassNotFound, "does not hold"},
		{500, provider.ClassProviderError, "may have been applied"},
		{503, provider.ClassUnreachable, "may have been applied"},
	}
	failures := map[string]func(*http.Request) (*http.Response, error){
		"timeout": func(*http.Request) (*http.Response, error) { return nil, timeoutError{} },
		"reset":   func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"garbage": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
		"error result": func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"result":"error"}`), nil
		},
	}
	for _, tt := range dropboxCases() {
		for _, s := range statuses {
			t.Run(fmt.Sprintf("%s %d", tt.name, s.status), func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
					return jsonResponse(s.status, `{"result":"error","error":{"description":"`+foreignCanary+" "+passwordCanary+`"}}`), nil
				}), nil)
				_, err := env.invokeConfirmed(tt.tool, "dropbox", tt.args)
				if classOf(err) != s.class || err == nil || !strings.Contains(err.Error(), s.want) ||
					strings.Contains(err.Error(), foreignCanary) || strings.Contains(err.Error(), passwordCanary) ||
					len(calls) != 2 {
					t.Fatalf("err = %v, calls = %d, want class %s mentioning %q after one request", err, len(calls), s.class, s.want)
				}
			})
		}
		for name, failure := range failures {
			t.Run(tt.name+" "+name, func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, failure), nil)
				_, err := env.invokeConfirmed(tt.tool, "dropbox", tt.args)
				if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(calls) != 2 ||
					strings.Contains(err.Error(), passwordCanary) {
					t.Fatalf("err = %v, calls = %d, want the uncertainty after one request", err, len(calls))
				}
			})
		}
		t.Run(tt.name+" asynchronous", func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, `{"result":"asynchronous","data":null}`), nil
			}), nil)
			result, err := env.invokeConfirmed(tt.tool, "dropbox", tt.args)
			var out DropboxResult
			if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.Status != statusPending || len(calls) != 2 {
				t.Fatalf("result = %s, %v, calls = %d, want pending", result, err, len(calls))
			}
		})
	}
}

func TestDropboxGetReadsOneDropboxAndDropsUnsafeURL(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, envelopeSuccess(dropboxJSONOf(dropboxURL))), nil
	}), nil)
	result, err := env.invoke(dropboxGet.ID, "dropbox", dropboxArgs(""))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if len(calls) != 2 || calls[1].method != http.MethodGet || calls[1].path != dropboxPathOf(childFileID) ||
		calls[1].query.Encode() != "with=capabilities" || calls[1].body != "" {
		t.Fatalf("calls = %+v", calls)
	}
	var out DropboxResult
	if err := json.Unmarshal([]byte(result), &out); err != nil || out.Status != "" || out.Dropbox == nil ||
		out.Dropbox.URL != dropboxURL || out.Dropbox.Name != "Invoices" || out.Dropbox.UsersCount != 3 ||
		!out.Dropbox.HasPassword || !out.Dropbox.HasValidity || out.Dropbox.CreatedAt != "2025-01-01T00:00:00Z" ||
		out.Dropbox.LastUploadedAt != "" {
		t.Fatalf("result = %s, %v", result, err)
	}
	for name, bad := range map[string]string{"http": "http://x.example/a", "javascript": "javascript:alert(1)",
		"too long": "https://x.example/" + strings.Repeat("a", 2000), "empty": ""} {
		t.Run(name, func(t *testing.T) {
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, envelopeSuccess(dropboxJSONOf(bad))), nil
			}), nil)
			result, err := env.invoke(dropboxGet.ID, "dropbox", dropboxArgs(""))
			var out DropboxResult
			if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.Dropbox == nil || out.Dropbox.URL != "" {
				t.Fatalf("result = %s, %v, want the dropbox without its URL", result, err)
			}
		})
	}
}

func TestDropboxGetFailures(t *testing.T) {
	var calls []call
	for status, class := range map[int]provider.Class{404: provider.ClassNotFound, 403: provider.ClassPermission} {
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
			return jsonResponse(status, `{"result":"error"}`), nil
		}), nil)
		if _, err := env.invoke(dropboxGet.ID, "dropbox", dropboxArgs("")); classOf(err) != class {
			t.Fatalf("status %d: err = %v, want %s", status, err, class)
		}
	}
	for name, answer := range map[string]string{"no id": envelopeSuccess(`{"uuid":"x"}`), "error": `{"result":"error"}`, "garbage": `not json`} {
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, answer), nil
		}), nil)
		if _, err := env.invoke(dropboxGet.ID, "dropbox", dropboxArgs("")); classOf(err) != provider.ClassInvalidResponse {
			t.Fatalf("%s: err = %v, want an invalid response", name, err)
		}
	}
}

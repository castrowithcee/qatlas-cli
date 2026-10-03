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

type trashCase struct {
	name, tool, args, method, path, body, success string
}

func trashCases() []trashCase {
	return []trashCase{
		{"trash", filesTrash.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, ownDrive, childFileID), http.MethodDelete,
			fmt.Sprintf("/2/drive/%d/files/%d", ownDrive, childFileID), "",
			envelopeSuccess(`{"cancel_id":"cancel-abc","valid_until":1735776000}`)},
		{"restore", trashRestore.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"destination_id":%d}`, ownDrive, childFileID, destFolderID),
			http.MethodPost, fmt.Sprintf("/2/drive/%d/trash/%d/restore", ownDrive, childFileID),
			fmt.Sprintf(`{"destination_directory_id":%d}`, destFolderID), envelopeSuccess(`true`)},
		{"delete", trashDelete.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, ownDrive, childFileID), http.MethodDelete,
			fmt.Sprintf("/2/drive/%d/trash/%d", ownDrive, childFileID), "", envelopeSuccess(`true`)},
		{"empty", trashEmpty.ID, fmt.Sprintf(`{"drive_id":%d}`, ownDrive), http.MethodDelete,
			fmt.Sprintf("/2/drive/%d/trash", ownDrive), "", envelopeSuccess(`true`)},
	}
}

func TestTrashDescriptors(t *testing.T) {
	want := map[string]struct {
		effect capability.Effect
		list   bool
	}{filesTrash.ID: {capability.EffectDelete, true}, trashRestore.ID: {capability.EffectUpdate, false},
		trashDelete.ID: {capability.EffectDelete, true}, trashEmpty.ID: {capability.EffectDelete, true}}
	for _, d := range []capability.Descriptor{filesTrash, trashRestore, trashDelete, trashEmpty} {
		w := want[d.ID]
		r := d.Risk
		if r.Effect != w.effect || d.RequiresToolAllowList != w.list || r.Idempotency != capability.IdempotencyNonIdempotent ||
			r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity == "" {
			t.Errorf("%s = %+v list=%v", d.ID, r, d.RequiresToolAllowList)
		}
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == filesTrash.ID || id == trashRestore.ID || id == trashDelete.ID || id == trashEmpty.ID {
				t.Errorf("profile %s contains %s", profile.ID, id)
			}
		}
	}
}

func TestTrashChangesSendExactlyOneRequest(t *testing.T) {
	for _, tt := range trashCases() {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(200, tt.success), nil
			}), nil)
			result, err := env.invokeConfirmed(tt.tool, "trash", tt.args)
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			if len(calls) != 2 || calls[0].path != ownershipPath(ownDrive) {
				t.Fatalf("calls = %+v, want the ownership check then one request", calls)
			}
			got := calls[1]
			if got.method != tt.method || got.path != tt.path || got.body != tt.body || len(got.query) != 0 {
				t.Fatalf("call = %+v, want %s %s body %q", got, tt.method, tt.path, tt.body)
			}
			var change Change
			if err := json.Unmarshal([]byte(result), &change); err != nil || change.Status != statusDone ||
				change.DriveID != ownDrive || change.Entry != nil {
				t.Fatalf("result = %s, %v", result, err)
			}
			if tt.name == "trash" && change.CancelID != "cancel-abc" {
				t.Fatalf("result = %s, want the cancel handle", result)
			}
		})
	}
}

func TestTrashChangesNeedConfirmationAndToolList(t *testing.T) {
	for _, tt := range trashCases() {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			_, err := env.invoke(tt.tool, "trash", tt.args)
			var needed *application.ConfirmationRequiredError
			if !errors.As(err, &needed) {
				t.Fatalf("err = %v, want confirmation required", err)
			}
			for _, connection := range []string{"trashunlisted", "drive", "readonly"} {
				if tt.name == "restore" && connection != "readonly" {
					continue // restore needs only the update permission, never the tool list
				}
				if _, err := env.invokeConfirmed(tt.tool, connection, tt.args); err == nil {
					t.Fatalf("connection %s ran %s", connection, tt.name)
				}
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

func TestTrashChangesRefuseForeignDrives(t *testing.T) {
	for _, tt := range trashCases() {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			foreign := strings.Replace(tt.args, fmt.Sprintf(`"drive_id":%d`, ownDrive),
				fmt.Sprintf(`"drive_id":%d`, foreignDrive), 1)
			_, err := env.invokeConfirmed(tt.tool, "trash", foreign)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), fmt.Sprint(foreignDrive)) {
				t.Fatalf("err = %v, want an invalid request without the foreign value", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
			calls = nil
			env = newEnvironment(t, &calls, withOwnership(foreignDrive, otherAccount, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("change request %s reached a drive of another account", r.URL.Path)
				return nil, nil
			}), nil)
			_, err = env.invokeConfirmed(tt.tool, "trashforeign", foreign)
			if !isInvalidRequest(err) || len(calls) != 1 || calls[0].method != http.MethodGet {
				t.Fatalf("err = %v, calls = %+v, want only the ownership check and a refusal", err, calls)
			}
		})
	}
}

func TestTrashChangesValidateIdentifiers(t *testing.T) {
	cases := []struct{ name, tool, args string }{
		{"root trash", filesTrash.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1}`, ownDrive)},
		{"zero trash", filesTrash.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":0}`, ownDrive)},
		{"negative delete", trashDelete.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":-4}`, ownDrive)},
		{"string restore", trashRestore.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":"5/../9","destination_id":3}`, ownDrive)},
		{"missing destination", trashRestore.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":5}`, ownDrive)},
		{"zero destination", trashRestore.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":5,"destination_id":0}`, ownDrive)},
		{"huge destination", trashRestore.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":5,"destination_id":9999999999999999999}`, ownDrive)},
		{"file on empty", trashEmpty.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":5}`, ownDrive)},
		{"extra on trash", filesTrash.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":5,"permanent":true}`, ownDrive)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			if _, err := env.invokeConfirmed(tt.tool, "trash", tt.args); err == nil {
				t.Fatal("a malformed change was accepted")
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

func TestTrashChangeErrorsAndUncertainOutcomesAreNeverRepeated(t *testing.T) {
	statuses := []struct {
		status int
		class  provider.Class
		want   string
	}{
		{403, provider.ClassPermission, "plan"},
		{500, provider.ClassProviderError, "may have been applied"},
		{503, provider.ClassUnreachable, "may have been applied"},
	}
	failures := map[string]func(*http.Request) (*http.Response, error){
		"timeout": func(*http.Request) (*http.Response, error) { return nil, timeoutError{} },
		"garbage": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
		"error result": func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"result":"error"}`), nil
		},
	}
	for _, tt := range trashCases() {
		for _, s := range statuses {
			t.Run(fmt.Sprintf("%s %d", tt.name, s.status), func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
					return jsonResponse(s.status, `{"result":"error","error":{"description":"`+foreignCanary+`"}}`), nil
				}), nil)
				_, err := env.invokeConfirmed(tt.tool, "trash", tt.args)
				if classOf(err) != s.class || err == nil || !strings.Contains(err.Error(), s.want) ||
					strings.Contains(err.Error(), foreignCanary) || len(calls) != 2 {
					t.Fatalf("err = %v, calls = %d, want class %s mentioning %q after one request", err, len(calls), s.class, s.want)
				}
			})
		}
		for name, failure := range failures {
			t.Run(tt.name+" "+name, func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, failure), nil)
				_, err := env.invokeConfirmed(tt.tool, "trash", tt.args)
				if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(calls) != 2 {
					t.Fatalf("err = %v, calls = %d, want the uncertainty after one request", err, len(calls))
				}
			})
		}
		t.Run(tt.name+" asynchronous", func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(200, `{"result":"asynchronous","data":{"cancel_id":"cancel-xyz","valid_until":1735776000}}`), nil
			}), nil)
			result, err := env.invokeConfirmed(tt.tool, "trash", tt.args)
			var change Change
			if err != nil || json.Unmarshal([]byte(result), &change) != nil || change.Status != statusPending ||
				change.CancelID != "cancel-xyz" || len(calls) != 2 {
				t.Fatalf("result = %s, %v, calls = %d, want pending", result, err, len(calls))
			}
		})
	}
}

func trashItem(id int64, name string, deleted int64) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"type":"file","parent_id":0,"status":"trashed",`+
		`"last_modified_at":1735776000,"deleted_at":%d}`, id, name, deleted)
}

func TestTrashListPaginatesAndBoundsStrings(t *testing.T) {
	var calls []call
	long := strings.Repeat("n", 5000)
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"success","data":[`+trashItem(7, long, 1735862400)+`,`+
			trashItem(8, "b.txt", 0)+`],"cursor":"next-page","has_more":true}`), nil
	}), nil)
	result, err := env.invoke(trashListDescriptor.ID, "drive",
		fmt.Sprintf(`{"drive_id":%d,"cursor":"abc","limit":20}`, ownDrive))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if len(calls) != 2 || calls[1].method != http.MethodGet || calls[1].path != fmt.Sprintf("/3/drive/%d/trash", ownDrive) ||
		calls[1].query.Get("cursor") != "abc" || calls[1].query.Get("limit") != "20" {
		t.Fatalf("calls = %+v", calls)
	}
	var page TrashPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != 2 || !page.HasMore ||
		page.Cursor != "next-page" || page.Entries[0].DeletedAt != "2025-01-03T00:00:00Z" ||
		page.Entries[1].DeletedAt != "" || len(page.Entries[0].Name) >= len(long) {
		t.Fatalf("page = %s, %v", result, err)
	}

	calls = nil
	if _, err := env.invoke(trashListDescriptor.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, ownDrive)); err != nil ||
		calls[1].query.Get("limit") != "10" || calls[1].query.Has("cursor") {
		t.Fatalf("default call = %+v, %v", calls, err)
	}
}

func TestTrashListRefusesForeignDriveAndPermissionFailure(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s", r.URL.Path)
		return nil, nil
	}, nil)
	_, err := env.invoke(trashListDescriptor.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, foreignDrive))
	if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d", err, calls, *env.reads)
	}
	env = newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"result":"error"}`), nil
	}), nil)
	_, err = env.invoke(trashListDescriptor.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, ownDrive))
	if classOf(err) != provider.ClassPermission {
		t.Fatalf("err = %v, want permission", err)
	}
}

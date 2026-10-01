package infomaniakdrive

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const destFolderID int64 = 77

func pathOf(op string, ids ...any) string {
	switch op {
	case "create":
		return fmt.Sprintf("/3/drive/%d/files/%d/directory", ownDrive, ids[0])
	case "rename":
		return fmt.Sprintf("/2/drive/%d/files/%d/rename", ownDrive, ids[0])
	default:
		return fmt.Sprintf("/3/drive/%d/files/%d/%s/%d", ownDrive, ids[0], op, ids[1])
	}
}

// changeCases lists the four changes with the arguments and the one request each must send.
func changeCases() []struct {
	name, tool, args, path, body string
} {
	return []struct{ name, tool, args, path, body string }{
		{"create", foldersCreate.ID, fmt.Sprintf(`{"drive_id":%d,"parent_id":%d,"name":"Reports"}`, ownDrive, rootID),
			pathOf("create", rootID), `{"name":"Reports"}`},
		{"rename", filesRename.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"name":"new.pdf"}`, ownDrive, childFileID),
			pathOf("rename", childFileID), `{"name":"new.pdf"}`},
		{"move", filesMove.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"destination_id":%d}`, ownDrive, childFileID, destFolderID),
			pathOf("move", childFileID, destFolderID), `{"conflict":"error"}`},
		{"copy", filesCopy.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"destination_id":%d}`, ownDrive, childFileID, destFolderID),
			pathOf("copy", childFileID, destFolderID), `{"conflict":"error"}`},
	}
}

func successFor(name string) string {
	switch name {
	case "create":
		return envelopeSuccess(fileJSONOf(300, rootID, "Reports", "dir"))
	case "copy":
		return envelopeSuccess(fileJSONOf(301, destFolderID, "copy.pdf", "file"))
	default:
		return envelopeSuccess(`{"cancel_id":"cancel-abc","valid_until":1735776000}`)
	}
}

// Every change sends the check of the drive and exactly one POST to its fixed path with its fixed body.
func TestChangesSendExactlyOneRequestToTheirFixedPath(t *testing.T) {
	for _, tt := range changeCases() {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(200, successFor(tt.name)), nil
			}), nil)
			result, err := env.invokeConfirmed(tt.tool, "drive", tt.args)
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			if len(calls) != 2 || calls[0].method != http.MethodGet || calls[0].path != ownershipPath(ownDrive) {
				t.Fatalf("calls = %+v, want the ownership check first", calls)
			}
			got := calls[1]
			if got.method != http.MethodPost || got.path != tt.path || got.body != tt.body || len(got.query) != 0 {
				t.Fatalf("change call = %+v, want POST %s body %q without a query", got, tt.path, tt.body)
			}
			if got.contentType != "application/json" {
				t.Fatalf("content type = %q", got.contentType)
			}
			var change Change
			if err := json.Unmarshal([]byte(result), &change); err != nil || change.Status != statusDone ||
				change.DriveID != ownDrive {
				t.Fatalf("result = %s, %v", result, err)
			}
			if tt.name == "rename" || tt.name == "move" {
				if change.CancelID != "cancel-abc" || change.ValidUntil == "" {
					t.Fatalf("result = %s, want the cancel handle", result)
				}
			} else if change.Entry == nil {
				t.Fatalf("result = %s, want the entry", result)
			}
		})
	}
}

// A change without confirmation, or on a connection that holds only the default read permission, reaches
// neither the secret store nor Infomaniak.
func TestChangesNeedConfirmationAndPermission(t *testing.T) {
	for _, tt := range changeCases() {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			_, err := env.invoke(tt.tool, "drive", tt.args)
			var needed *application.ConfirmationRequiredError
			if !errors.As(err, &needed) {
				t.Fatalf("err = %v, want confirmation required", err)
			}
			if _, err := env.invokeConfirmed(tt.tool, "readonly", tt.args); err == nil {
				t.Fatal("a read-only connection ran a change")
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, secret reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

// A drive outside the allow-list, and a drive of another account, are refused; the first before any secret
// or request, the second before the change request. Neither names the foreign value.
func TestChangesRefuseForeignDrives(t *testing.T) {
	for _, tt := range changeCases() {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			foreign := strings.Replace(tt.args, fmt.Sprintf(`"drive_id":%d`, ownDrive),
				fmt.Sprintf(`"drive_id":%d`, foreignDrive), 1)
			_, err := env.invokeConfirmed(tt.tool, "drive", foreign)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), fmt.Sprint(foreignDrive)) {
				t.Fatalf("err = %v, want an invalid request without the foreign value", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, secret reads = %d, want none", calls, *env.reads)
			}

			// The allow-list names a drive that really belongs to another account.
			calls = nil
			env = newEnvironment(t, &calls, withOwnership(foreignDrive, otherAccount, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("change request %s reached a drive of another account", r.URL.Path)
				return nil, nil
			}), nil)
			_, err = env.invokeConfirmed(tt.tool, "driveforeign", foreign)
			if !isInvalidRequest(err) || len(calls) != 1 || calls[0].method != http.MethodGet {
				t.Fatalf("err = %v, calls = %+v, want only the ownership check and a refusal", err, calls)
			}
		})
	}
}

// Identifiers and names are validated before any secret or request, and a refusal never quotes the value.
func TestChangesValidateIdentifiersAndNames(t *testing.T) {
	long := strings.Repeat("a", 256)
	multibyte := strings.Repeat("ä", 128) // 128 characters, 256 bytes
	cases := []struct{ name, tool, args string }{
		{"root rename", filesRename.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1,"name":"x"}`, ownDrive)},
		{"root move", filesMove.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1,"destination_id":5}`, ownDrive)},
		{"root copy", filesCopy.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1,"destination_id":5}`, ownDrive)},
		{"same destination", filesMove.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":5,"destination_id":5}`, ownDrive)},
		{"zero file", filesMove.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":0,"destination_id":5}`, ownDrive)},
		{"negative destination", filesCopy.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":5,"destination_id":-3}`, ownDrive)},
		{"huge id", filesCopy.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":5,"destination_id":9999999999999999999}`, ownDrive)},
		{"string id", filesCopy.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":"5/../9","destination_id":6}`, ownDrive)},
		{"slash name", foldersCreate.ID, fmt.Sprintf(`{"drive_id":%d,"parent_id":1,"name":"a/b"}`, ownDrive)},
		{"long name", filesRename.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":5,"name":%q}`, ownDrive, long)},
		{"multibyte over 255 bytes", filesRename.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":5,"name":%q}`, ownDrive, multibyte)},
		{"empty name", foldersCreate.ID, fmt.Sprintf(`{"drive_id":%d,"parent_id":1,"name":""}`, ownDrive)},
		{"control name", foldersCreate.ID, fmt.Sprintf(`{"drive_id":%d,"parent_id":1,"name":"a\nb"}`, ownDrive)},
		{"dot name", foldersCreate.ID, fmt.Sprintf(`{"drive_id":%d,"parent_id":1,"name":".."}`, ownDrive)},
		{"missing parent", foldersCreate.ID, fmt.Sprintf(`{"drive_id":%d,"name":"x"}`, ownDrive)},
		{"conflict version", filesCopy.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":5,"destination_id":6,"conflict":"version"}`, ownDrive)},
		{"free conflict value", filesMove.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":5,"destination_id":6,"conflict":"overwrite"}`, ownDrive)},
		{"free name on move", filesMove.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":5,"destination_id":6,"name":"x"}`, ownDrive)},
		{"relative path on create", foldersCreate.ID, fmt.Sprintf(`{"drive_id":%d,"parent_id":1,"name":"x","relative_path":"a/b"}`, ownDrive)},
		{"free conflict argument", foldersCreate.ID, fmt.Sprintf(`{"drive_id":%d,"parent_id":1,"name":"x","conflict":"rename"}`, ownDrive)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			_, err := env.invokeConfirmed(tt.tool, "drive", tt.args)
			if err == nil {
				t.Fatal("a malformed change was accepted")
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, secret reads = %d, want none", calls, *env.reads)
			}
		})
	}
	if !validName("Ünïcode name.txt") || !validName(strings.Repeat("a", 255)) {
		t.Fatal("a valid name was refused")
	}
}

// A 409 is a conflict that changed nothing; a 403 is a permission failure that names the plan; a 429 is
// rate-limited. None of them repeats the request, none leaks the provider body.
func TestChangeErrorsAreClassifiedAndNeverRepeated(t *testing.T) {
	cases := []struct {
		status int
		class  provider.Class
		want   string
		unsure bool
	}{
		{409, provider.ClassProviderError, "conflict", false},
		{403, provider.ClassPermission, "plan", false},
		{429, provider.ClassRateLimited, "60 requests per minute", false},
		{500, provider.ClassProviderError, "may have been applied", true},
		{503, provider.ClassUnreachable, "may have been applied", true},
	}
	for _, tt := range cases {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(tt.status, `{"result":"error","error":{"description":"`+foreignCanary+`"}}`), nil
			}), nil)
			c := changeCases()[1]
			_, err := env.invokeConfirmed(c.tool, "drive", c.args)
			if classOf(err) != tt.class || err == nil || !strings.Contains(err.Error(), tt.want) ||
				strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("err = %v, want class %s mentioning %q", err, tt.class, tt.want)
			}
			if strings.Contains(err.Error(), "may have been applied") != tt.unsure {
				t.Fatalf("err = %v, uncertainty wrong", err)
			}
			if len(calls) != 2 {
				t.Fatalf("calls = %d, want the check and exactly one change request", len(calls))
			}
		})
	}
}

// A timeout, a dropped connection, and an unreadable answer report the uncertainty after exactly one change
// request.
func TestChangeUncertainOutcomesAreReportedWithoutRepeating(t *testing.T) {
	failures := map[string]func(*http.Request) (*http.Response, error){
		"timeout": func(*http.Request) (*http.Response, error) { return nil, timeoutError{} },
		"reset":   func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"garbage": func(*http.Request) (*http.Response, error) { return jsonResponse(200, `not json`), nil },
		"error result": func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"result":"error"}`), nil
		},
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, failure), nil)
			c := changeCases()[0]
			_, err := env.invokeConfirmed(c.tool, "drive", c.args)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") {
				t.Fatalf("err = %v, want the uncertainty", err)
			}
			if len(calls) != 2 {
				t.Fatalf("calls = %d, want the check and exactly one change request", len(calls))
			}
		})
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// An asynchronous answer is an accepted, pending change with its cancel handle, not a failure.
func TestAsynchronousAnswerIsPendingNotAnError(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"asynchronous","data":{"cancel_id":"cancel-xyz","valid_until":1735776000}}`), nil
	}), nil)
	c := changeCases()[3]
	result, err := env.invokeConfirmed(c.tool, "drive", c.args)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var change Change
	if err := json.Unmarshal([]byte(result), &change); err != nil || change.Status != statusPending ||
		change.CancelID != "cancel-xyz" || change.Entry != nil {
		t.Fatalf("result = %s, %v, want a pending change with its cancel handle", result, err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want exactly one change request", len(calls))
	}
}

// Reads still refuse an asynchronous answer.
func TestReadsStillRefuseAsynchronous(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"asynchronous","data":{}}`), nil
	}), nil)
	_, err := env.invoke(filesStat.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, ownDrive))
	if classOf(err) != provider.ClassInvalidResponse {
		t.Fatalf("err = %v, want invalid-provider-response", err)
	}
}

// conflict rename is allowed for a move and a copy and sent as given.
func TestTransfersSendTheChosenConflictMode(t *testing.T) {
	for _, tt := range changeCases()[2:] {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(200, successFor(tt.name)), nil
			}), nil)
			args := strings.TrimSuffix(tt.args, "}") + `,"conflict":"rename"}`
			if _, err := env.invokeConfirmed(tt.tool, "drive", args); err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			if len(calls) != 2 || calls[1].body != `{"conflict":"rename"}` {
				t.Fatalf("calls = %+v, want the conflict mode rename", calls)
			}
		})
	}
}

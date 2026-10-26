package infomaniakdrive

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const originalAnswer = `{"result":"error","error":{"code":"validation_failed","description":"Validation failed",` +
	`"errors":[{"code":"attribute_required","description":"name is required","context":{"attribute":"name"}},` +
	`{"code":"name_taken","description":"name is taken"}]}}`

var originalEntries = []provider.OriginalEntry{
	{Code: "validation_failed", Message: "Validation failed"},
	{Code: "attribute_required", Message: "name is required"},
	{Code: "name_taken", Message: "name is taken"},
}

func originalOf(t *testing.T, err error) *provider.Original {
	t.Helper()
	if err == nil {
		t.Fatal("err = nil, want a provider error")
	}
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Original == nil {
		t.Fatalf("err = %v, want a provider error with original details", err)
	}
	return failure.Original
}

func wantOriginal(t *testing.T, err error, status int, entries []provider.OriginalEntry) {
	t.Helper()
	original := originalOf(t, err)
	if original.Status != status || !reflect.DeepEqual(original.Entries, entries) {
		t.Fatalf("Original = %+v, want status %d and entries %+v", original, status, entries)
	}
	if strings.Contains(err.Error(), "Validation failed") || strings.Contains(err.Error(), "name is") {
		t.Fatalf("error %v carries the provider text", err)
	}
}

// Every failing status keeps its class and message and carries Infomaniak's answer as original details.
func TestFailedStatusesCarryOriginalDetails(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 422, 429, 500, 503} {
		for _, change := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d change=%v", status, change), func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
					return jsonResponse(status, originalAnswer), nil
				}), nil)
				var err error
				if change {
					c := changeCases()[1]
					_, err = env.invokeConfirmed(c.tool, "drive", c.args)
				} else {
					_, err = env.invoke(filesList.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, ownDrive))
				}
				wantOriginal(t, err, status, originalEntries)
			})
		}
	}
}

func TestEmptyAndPlainBodiesKeepStatusAndText(t *testing.T) {
	for body, want := range map[string][]provider.OriginalEntry{
		"":                      nil,
		"upstream unavailable ": {{Message: "upstream unavailable"}},
	} {
		var calls []call
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
			return jsonResponse(502, body), nil
		}), nil)
		_, err := env.invoke(filesList.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, ownDrive))
		wantOriginal(t, err, 502, want)
	}
}

// A 2xx answer with another result than success keeps the class and message and adds the status and error.
func TestUnsuccessfulResultCarriesOriginalDetails(t *testing.T) {
	answer := func(*http.Request) (*http.Response, error) { return jsonResponse(200, originalAnswer), nil }
	check := func(t *testing.T, err error) {
		t.Helper()
		if classOf(err) != provider.ClassInvalidResponse ||
			!strings.Contains(err.Error(), "Infomaniak reported an error for a response with an HTTP success status") {
			t.Fatalf("err = %v, want the unchanged invalid-response message", err)
		}
		wantOriginal(t, err, 200, originalEntries)
	}
	t.Run("read", func(t *testing.T) {
		var calls []call
		env := newEnvironment(t, &calls, answer, nil)
		_, err := env.invoke(filesList.ID, "drive", fmt.Sprintf(`{"drive_id":%d}`, ownDrive))
		check(t, err)
	})
	t.Run("change", func(t *testing.T) {
		var calls []call
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, answer), nil)
		c := changeCases()[0]
		_, err := env.invokeConfirmed(c.tool, "drive", c.args)
		check(t, err)
	})
	t.Run("link", func(t *testing.T) {
		var calls []call
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, answer), nil)
		c := linkCases()[0]
		_, err := env.invokeConfirmed(c.tool, "links", c.args)
		check(t, err)
	})
	t.Run("dropbox", func(t *testing.T) {
		var calls []call
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, answer), nil)
		c := dropboxCases()[0]
		_, err := env.invokeConfirmed(c.tool, "dropbox", c.args)
		check(t, err)
	})
	t.Run("access", func(t *testing.T) {
		var calls []call
		env := newEnvironment(t, &calls, membership(answer), nil)
		c := accessCases()[0]
		_, err := env.invokeConfirmed(c.tool, "access", c.args)
		check(t, err)
	})
	t.Run("upload", func(t *testing.T) {
		var calls []call
		env := uploadEnvironment(t, t.TempDir(), &calls, directFake(t, 200, originalAnswer), nil)
		args := fmt.Sprintf(`{"drive_id":%d,"directory_id":%d,"name":"a.txt","content_base64":"eA=="}`, ownDrive, rootID)
		_, err := mustInvoke(t, env, "files-drive", args)
		check(t, err)
	})
}

// A refused etag keeps its own message and carries Infomaniak's answer as original details.
func TestEtagConflictCarriesOriginalDetails(t *testing.T) {
	for _, status := range []int{412, 409} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls []call
			env := uploadEnvironment(t, t.TempDir(), &calls, directFake(t, status, originalAnswer), nil)
			args := fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"etag":%q,"content_base64":"eA=="}`,
				ownDrive, childFileID, knownEtag)
			_, err := mustInvoke(t, env, "files-drive", args)
			if err == nil || !strings.Contains(err.Error(), "etag conflict") {
				t.Fatalf("err = %v, want an etag conflict", err)
			}
			wantOriginal(t, err, status, originalEntries)
		})
	}
}

// The download endpoint answers with Infomaniak's format; after a redirect the storage's answer keeps only
// the status.
func TestDownloadFailureOriginalDependsOnTheAnswerOrigin(t *testing.T) {
	for _, tt := range []struct {
		name    string
		host    string
		entries []provider.OriginalEntry
	}{
		{"own endpoint", apiHost, originalEntries},
		{"storage", "storage.example", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
				response := jsonResponse(404, originalAnswer)
				response.Request = &http.Request{URL: &url.URL{Scheme: "https", Host: tt.host, Path: r.URL.Path}}
				return response, nil
			}), nil)
			_, err := env.invoke(filesGet.ID, "drive", fmt.Sprintf(`{"drive_id":%d,"file_id":%d}`, ownDrive, childFileID))
			if classOf(err) != provider.ClassNotFound {
				t.Fatalf("err = %v, want not-found", err)
			}
			wantOriginal(t, err, 404, tt.entries)
		})
	}
}

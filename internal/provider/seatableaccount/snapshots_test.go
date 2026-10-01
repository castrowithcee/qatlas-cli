package seatableaccount

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func listBody(commits ...string) string {
	entries := make([]string, len(commits))
	for i, c := range commits {
		entries[i] = `{"dtable_name":"My Base","commit_id":"` + c + `","ctime":"2026-09-01T10:00:00+00:00"}`
	}
	return `{"snapshot_list":[` + strings.Join(entries, ",") + `],"page_info":{"has_next_page":false,"current_page":1}}`
}

const restoreAnswer = `{"dtable":{"id":7,"workspace_id":42,"uuid":"u","name":"My Base(snapshot)","creator":"c",` +
	`"modifier":"m","created_at":"x","updated_at":"y","color":null,"text_color":null,"icon":null}}`

func TestSnapshotsListSendsOnlyTheBoundPathAndBoundsTheResult(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"snapshot_list":[{"dtable_name":"`+strings.Repeat("n", 5000)+`","commit_id":"`+commitOne+
			`","ctime":"t"},{"dtable_name":"x","commit_id":"`+commitTwo+`","ctime":"t"}],`+
			`"page_info":{"has_next_page":true,"current_page":2}}`), nil
	})
	result, err := env.invoke(snapshotsList.ID, "reader", `{"page":2,"per_page":1}`, false)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var page SnapshotsPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || len(page.Snapshots) != 1 || !page.HasMore ||
		page.Page != 2 || len(page.Snapshots[0].BaseName) > maxStringLength {
		t.Fatalf("result = %s, %v", result, err)
	}
	if len(calls) != 1 || calls[0].method != http.MethodGet || calls[0].path != snapshotPath ||
		calls[0].escaped != "/api/v2.1/workspace/42/dtable/My%20Base/snapshots/" ||
		calls[0].query.Get("page") != "2" || calls[0].query.Get("per_page") != "1" || calls[0].auth != "Bearer "+tokenValue {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestSnapshotsListRejectsUnknownAndOutOfRangeArguments(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request %s", r.URL.Path)
		return nil, nil
	})
	for _, args := range []string{`{"workspace_id":"9"}`, `{"base":"x"}`, `{"page":0}`, `{"per_page":101}`, `{"url":"https://x"}`} {
		if _, err := env.invoke(snapshotsList.ID, "reader", args, false); err == nil {
			t.Errorf("arguments %s were accepted", args)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, reads = %d, want none", len(calls), *env.reads)
	}
}

func TestRestoreNeedsConfirmationToolsListAndPermission(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request %s", r.URL.Path)
		return nil, nil
	})
	args := `{"commit_id":"` + commitOne + `"}`
	_, err := env.invoke(snapshotsRestore.ID, "restorer", args, false)
	var confirmation *application.ConfirmationRequiredError
	if !errorsAs(err, &confirmation) {
		t.Fatalf("err = %v, want confirmation-required", err)
	}
	for _, connection := range []string{"reader", "broad"} {
		if _, err := env.invoke(snapshotsRestore.ID, connection, args, true); err == nil {
			t.Errorf("connection %s offered restore without a tools list naming it and create permission", connection)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, reads = %d, want none", len(calls), *env.reads)
	}
}

func TestRestoreRejectsBadCommitFormBeforeSecretAccess(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request %s", r.URL.Path)
		return nil, nil
	})
	for _, id := range []string{"", "short", "../../x", "abc/def/ghij", "0123456789abcdef%2f", strings.Repeat("a", 65), "abcd efgh12"} {
		if _, err := env.invoke(snapshotsRestore.ID, "restorer", `{"commit_id":"`+id+`"}`, true); err == nil {
			t.Errorf("commit_id %q was accepted", id)
		}
	}
	if _, err := env.invoke(snapshotsRestore.ID, "restorer", `{"commit_id":"`+commitOne+`","workspace_id":1}`, true); err == nil {
		t.Error("extra argument accepted")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("calls = %d, reads = %d, want none", len(calls), *env.reads)
	}
}

func TestRestoreRefusesACommitOutsideTheBoundBaseWithoutARestoreRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected %s request", r.Method)
		}
		return jsonResponse(200, listBody(commitOne)), nil
	})
	_, err := env.invoke(snapshotsRestore.ID, "restorer", `{"commit_id":"`+foreignCommt+`"}`, true)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), foreignCommt) {
		t.Fatalf("err = %v, want an invalid request that does not name the commit", err)
	}
	for _, c := range calls {
		if c.method != http.MethodGet {
			t.Fatalf("calls = %+v", calls)
		}
	}
}

func TestRestoreSendsExactlyOneRestoreRequestAndReturnsTheNewBase(t *testing.T) {
	var calls []call
	var sent string
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			data, _ := io.ReadAll(r.Body)
			sent = string(data)
			return jsonResponse(200, restoreAnswer), nil
		}
		return jsonResponse(200, listBody(commitOne, commitTwo)), nil
	})
	result, err := env.invoke(snapshotsRestore.ID, "restorer", `{"commit_id":"`+commitTwo+`"}`, true)
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	var restored RestoreResult
	if err := json.Unmarshal([]byte(result), &restored); err != nil || !restored.Restored ||
		restored.Base.ID != 7 || restored.Base.WorkspaceID != 42 || restored.Base.Name != "My Base(snapshot)" {
		t.Fatalf("result = %s, %v", result, err)
	}
	posts := 0
	for _, c := range calls {
		if c.method == http.MethodPost {
			posts++
			if c.path != snapshotPath+commitTwo+"/restore/" || c.auth != "Bearer "+tokenValue {
				t.Fatalf("restore call = %+v", c)
			}
		}
	}
	if posts != 1 || sent != "{}" {
		t.Fatalf("posts = %d, body = %q, want one request with an empty object", posts, sent)
	}
}

func TestRestoreFindsACommitOnALaterPage(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			return jsonResponse(200, restoreAnswer), nil
		}
		if r.URL.Query().Get("page") == "1" {
			return jsonResponse(200, `{"snapshot_list":[{"dtable_name":"a","commit_id":"`+commitOne+
				`","ctime":"t"}],"page_info":{"has_next_page":true,"current_page":1}}`), nil
		}
		return jsonResponse(200, listBody(commitTwo)), nil
	})
	if _, err := env.invoke(snapshotsRestore.ID, "restorer", `{"commit_id":"`+commitTwo+`"}`, true); err != nil {
		t.Fatalf("invoke() = %v", err)
	}
}

// A failure that could mean the restore arrived is reported as uncertain and never repeated.
func TestRestoreNeverRetriesAndReportsUncertainty(t *testing.T) {
	cases := map[string]func(*http.Request) (*http.Response, error){
		"5xx":        func(*http.Request) (*http.Response, error) { return jsonResponse(500, bodyCanary), nil },
		"unreadable": func(*http.Request) (*http.Response, error) { return jsonResponse(200, "not json"), nil },
		"no base":    func(*http.Request) (*http.Response, error) { return jsonResponse(200, `{}`), nil },
		"transport": func(*http.Request) (*http.Response, error) {
			return nil, io.ErrUnexpectedEOF
		},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPost {
					return answer(r)
				}
				return jsonResponse(200, listBody(commitOne)), nil
			})
			_, err := env.invoke(snapshotsRestore.ID, "restorer", `{"commit_id":"`+commitOne+`"}`, true)
			if err == nil || !strings.Contains(err.Error(), "may have created a base") || strings.Contains(err.Error(), bodyCanary) {
				t.Fatalf("err = %v, want an uncertainty message", err)
			}
			posts := 0
			for _, c := range calls {
				if c.method == http.MethodPost {
					posts++
				}
			}
			if posts != 1 {
				t.Fatalf("posts = %d, want exactly 1", posts)
			}
		})
	}
}

func TestRestoreClientErrorsAreNotMarkedUncertain(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			return jsonResponse(403, `{}`), nil
		}
		return jsonResponse(200, listBody(commitOne)), nil
	})
	_, err := env.invoke(snapshotsRestore.ID, "restorer", `{"commit_id":"`+commitOne+`"}`, true)
	if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), "may have created") {
		t.Fatalf("err = %v", err)
	}
}

func errorsAs(err error, target **application.ConfirmationRequiredError) bool {
	for err != nil {
		if e, ok := err.(*application.ConfirmationRequiredError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

package todoist

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const originalBody = `{"error_tag":"INVALID_ARGUMENT_VALUE","error_code":20,"error":"todoist-says-1",` +
	`"error_extra":{"argument":"cursor","bad_item":"echo-canary"},"http_code":400}`

func originalOf(t *testing.T, err error) *provider.Original {
	t.Helper()
	var failure *provider.Error
	if err == nil || !errors.As(err, &failure) {
		t.Fatalf("err = %v, want a provider error", err)
	}
	if strings.Contains(failure.Error(), "todoist-says-1") {
		t.Errorf("Error() = %q carries Todoist's text", failure.Error())
	}
	if failure.Original == nil {
		t.Fatalf("err = %v has no Original", err)
	}
	return failure.Original
}

func TestStatusErrorsCarryTheOriginalAnswer(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
		class  provider.Class
		want   provider.OriginalEntry
	}{
		{"unauthorized", 401, `{"error_tag":"UNAUTHORIZED","error_code":477,"error":"todoist-says-1","http_code":401}`,
			provider.ClassAuth, provider.OriginalEntry{Type: "UNAUTHORIZED", Code: "477", Message: "todoist-says-1"}},
		{"forbidden", 403, `{"error_tag":"FORBIDDEN","error_code":32,"error":"todoist-says-1"}`,
			provider.ClassPermission, provider.OriginalEntry{Type: "FORBIDDEN", Code: "32", Message: "todoist-says-1"}},
		{"not found", 404, `{"error_tag":"NOT_FOUND","error_code":478,"error":"todoist-says-1"}`,
			provider.ClassNotFound, provider.OriginalEntry{Type: "NOT_FOUND", Code: "478", Message: "todoist-says-1"}},
		{"invalid", 400, originalBody, provider.ClassProviderError,
			provider.OriginalEntry{Type: "INVALID_ARGUMENT_VALUE", Code: "20", Path: "cursor", Message: "todoist-says-1"}},
		{"unprocessable", 422, `{"error":"todoist-says-1"}`, provider.ClassProviderError,
			provider.OriginalEntry{Message: "todoist-says-1"}},
		{"server error", 502, `<html>bad gateway</html>`, provider.ClassProviderError,
			provider.OriginalEntry{Message: "<html>bad gateway</html>"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
				return true
			}}
			env := newEnvironment(t, f)
			_, err := env.invoke("todoist.tasks.get", "one", `{"task_id":"taskDue"}`)
			if classOf(err) != tt.class {
				t.Errorf("class = %q, want %q", classOf(err), tt.class)
			}
			original := originalOf(t, err)
			if original.Status != tt.status || len(original.Entries) != 1 || original.Entries[0] != tt.want {
				t.Errorf("Original = %+v, want status %d and %+v", original, tt.status, tt.want)
			}
			if strings.Contains(fmt.Sprintf("%+v", original), "echo-canary") {
				t.Errorf("Original = %+v carries error_extra beyond the argument", original)
			}
		})
	}
}

func TestACommandRefusalCarriesTheOriginalAnswer(t *testing.T) {
	var f *fakeTodoist
	f = &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/sync" {
			return false
		}
		requests := f.recorded()
		var commands []struct {
			UUID string `json:"uuid"`
		}
		_ = json.Unmarshal([]byte(requests[len(requests)-1].form.Get("commands")), &commands)
		if len(commands) != 1 {
			return false
		}
		fmt.Fprint(w, `{"sync_token":"abc","sync_status":{"`+commands[0].UUID+`":`+originalBody+`}}`)
		return true
	}}
	env := newEnvironment(t, f)
	_, err := env.change("todoist.sections.move", "multiorganizer",
		`{"section_id":"sectOwn","project_id":"`+otherProject+`"}`)
	original := originalOf(t, err)
	want := provider.OriginalEntry{Type: "INVALID_ARGUMENT_VALUE", Code: "20", Path: "cursor", Message: "todoist-says-1"}
	if original.Status != 400 || len(original.Entries) != 1 || original.Entries[0] != want {
		t.Errorf("Original = %+v, want status 400 and %+v", original, want)
	}
	if strings.Contains(fmt.Sprintf("%+v", original), "echo-canary") {
		t.Errorf("Original = %+v carries error_extra beyond the argument", original)
	}
}

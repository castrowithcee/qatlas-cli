package twentycrm

import (
	"bytes"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	originalCanaryOne = "original-canary-one-4c1"
	originalCanaryTwo = "original-canary-two-7a9"
)

func originalOf(t *testing.T, err error) *provider.Original {
	t.Helper()
	var failure *provider.Error
	if !errors.As(err, &failure) {
		t.Fatalf("err = %v, want a provider error", err)
	}
	return failure.Original
}

func restFailureBody(status int) string {
	return `{"statusCode":` + strconv.Itoa(status) + `,"error":"SomeException","code":"SOME_CODE",` +
		`"messages":["` + originalCanaryOne + `","` + originalCanaryTwo + `"]}`
}

var restWantEntries = []provider.OriginalEntry{
	{Type: "SomeException", Code: "SOME_CODE"},
	{Message: originalCanaryOne},
	{Message: originalCanaryTwo},
}

var restClasses = map[int]provider.Class{
	http.StatusBadRequest:          provider.ClassProviderError,
	http.StatusUnauthorized:        provider.ClassAuth,
	http.StatusForbidden:           provider.ClassPermission,
	http.StatusNotFound:            provider.ClassNotFound,
	http.StatusUnprocessableEntity: provider.ClassProviderError,
}

func checkRestOriginal(t *testing.T, label string, status int, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s %d: no error", label, status)
	}
	if classOf(err) == "" || strings.Contains(err.Error(), originalCanaryOne) ||
		strings.Contains(err.Error(), originalCanaryTwo) {
		t.Errorf("%s %d: err = %v", label, status, err)
	}
	original := originalOf(t, err)
	if original == nil || original.Status != status || !reflect.DeepEqual(original.Entries, restWantEntries) {
		t.Errorf("%s %d: original = %+v", label, status, original)
	}
}

func TestRESTFailuresCarryTheOriginalOfTwenty(t *testing.T) {
	for status, class := range restClasses {
		body := restFailureBody(status)
		serve(t, func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == schemaPath {
				return jsonResponse(http.StatusOK, writeSchema), nil
			}
			return jsonResponse(status, body), nil
		})
		stubLimiter(t, cloudKey)

		_, err := runTool(t, invokeRecordsGet, `{"object":"person","id":"`+personID+`"}`)
		checkRestOriginal(t, "get", status, err)
		if classOf(err) != class {
			t.Errorf("get %d: class = %q, want %q", status, classOf(err), class)
		}

		_, err = runWrite(t, false, `{"object":"person","id":"`+personID+`","fields":{"city":"x"}}`)
		checkRestOriginal(t, "change", status, err)
		if classOf(err) != class {
			t.Errorf("change %d: class = %q, want %q", status, classOf(err), class)
		}

		_, err = runTool(t, invokeWebhooksList, `{}`)
		checkRestOriginal(t, "webhooks", status, err)
		if classOf(err) != class {
			t.Errorf("webhooks %d: class = %q, want %q", status, classOf(err), class)
		}
	}
}

func TestRESTFailureKeepsOnlyTheStatusForAnEmptyOrForeignBody(t *testing.T) {
	for name, body := range map[string]string{"empty": "", "unnamed json": `{"detail":"` + originalCanaryOne + `"}`} {
		serve(t, func(request *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusForbidden, body), nil
		})
		stubLimiter(t, cloudKey)
		_, err := runTool(t, invokeWebhooksList, `{}`)
		original := originalOf(t, err)
		if original == nil || original.Status != http.StatusForbidden || len(original.Entries) != 0 {
			t.Errorf("%s: original = %+v", name, original)
		}
	}
}

func TestGraphQLFailuresCarryTheOriginalOfTwenty(t *testing.T) {
	for code, class := range map[string]provider.Class{
		"BAD_USER_INPUT":  provider.ClassProviderError,
		"UNAUTHENTICATED": provider.ClassAuth,
		"FORBIDDEN":       provider.ClassPermission,
	} {
		serveRoles(t, http.StatusOK, `{"data":null,"errors":[`+
			`{"message":"`+originalCanaryOne+`","path":["findManyAgents",0,"name"],"extensions":{"code":"`+code+`"}},`+
			`{"message":"`+originalCanaryTwo+`"}]}`)
		_, err := runMemberTool(t, invokeAgentsList, `{}`)
		if err == nil || classOf(err) != class || strings.Contains(err.Error(), originalCanaryOne) ||
			strings.Contains(err.Error(), originalCanaryTwo) {
			t.Fatalf("%s: err = %v", code, err)
		}
		want := &provider.Original{Status: http.StatusOK, Entries: []provider.OriginalEntry{
			{Code: code, Path: "findManyAgents.0.name", Message: originalCanaryOne},
			{Message: originalCanaryTwo},
		}}
		if got := originalOf(t, err); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: original = %+v, want %+v", code, got, want)
		}
	}
}

func TestStorageFailuresKeepOnlyTheStatus(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		fake := okFake("x")
		fake.download = func() *http.Response {
			return &http.Response{StatusCode: status, Header: http.Header{},
				Body: readCloser{bytes.NewReader([]byte(`{"messages":["` + originalCanaryOne + `"]}`))}}
		}
		serveAttachments(t, fake)
		_, err := runAttachment(t, t.Context(), targetConnection(), true, getArgs(""))
		if err == nil {
			t.Fatalf("download %d: no error", status)
		}
		if original := originalOf(t, err); original == nil || original.Status != status || len(original.Entries) != 0 {
			t.Errorf("download %d: original = %+v", status, original)
		}

		upload := &uploadFake{put: func(*http.Request) (*http.Response, error) {
			return jsonResponse(status, `{"messages":["`+originalCanaryOne+`"]}`), nil
		}}
		serveUpload(t, upload)
		_, _, err = runUpload(t, uploadConnection("", "object/person"), inlineArgs(uploadBody))
		if err == nil {
			t.Fatalf("put %d: no error", status)
		}
		if original := originalOf(t, err); original == nil || original.Status != status || len(original.Entries) != 0 {
			t.Errorf("put %d: original = %+v", status, original)
		}
	}
}

func TestLocalNotFoundCarriesNoOriginal(t *testing.T) {
	serveRoles(t, http.StatusOK, `{"data":{"skill":null}}`)
	_, err := runMemberTool(t, invokeSkillsGet, `{"id":"`+roleID+`"}`)
	if classOf(err) != provider.ClassNotFound || originalOf(t, err) != nil {
		t.Errorf("skill: err = %v", err)
	}

	serveWidgets(t, http.StatusCreated, widgetWire(wNewWidget, layoutTabID, "Notes", "STANDALONE_RICH_TEXT", "", wGrid))
	_, err = runColumn(invokePageLayoutWidgetsCreate, tableArgs(wViewDeleted))
	if classOf(err) != provider.ClassNotFound || originalOf(t, err) != nil {
		t.Errorf("deleted view: err = %v", err)
	}
}

func TestForeignNotFoundOfADeeperReadKeepsTheOriginal(t *testing.T) {
	serveWidgets(t, http.StatusCreated, widgetWire(wNewWidget, layoutTabID, "Notes", "STANDALONE_RICH_TEXT", "", wGrid))
	_, err := runColumn(invokePageLayoutWidgetsCreate, strings.Replace(richArgs("x"), layoutTabID, wTabMissing, 1))
	if classOf(err) != provider.ClassNotFound {
		t.Fatalf("err = %v", err)
	}
	if original := originalOf(t, err); original == nil || original.Status != http.StatusNotFound {
		t.Errorf("original = %+v", original)
	}
}

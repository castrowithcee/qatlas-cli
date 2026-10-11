package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

func profileFake(calls *[]recorded, status int, body string) roundTripFunc {
	return func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		*calls = append(*calls, recorded{r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], string(data)})
		return response(status, body), nil
	}
}

func TestProfileSettersSendFixedMethodAndBody(t *testing.T) {
	for _, tt := range []struct {
		tool, method, field string
		value, lang, body   string
	}{
		{"setname", "setMyName", "name", "Helper", "", `{"name":"Helper"}`},
		{"setname", "setMyName", "name", "Helfer", "de", `{"language_code":"de","name":"Helfer"}`},
		{"setdescription", "setMyDescription", "description", "", "de", `{"description":"","language_code":"de"}`},
		{"setshortdescription", "setMyShortDescription", "short_description", "", "", `{"short_description":""}`},
	} {
		var calls []recorded
		client, _ := multiClient(t, testToken, profileFake(&calls, 200, `{"ok":true,"result":true}`), "bot")
		out, err := client.setProfileText(context.Background(), "op", tt.method, tt.field, tt.value, tt.lang)
		if err != nil || out["updated"] != true {
			t.Fatalf("%v %v", out, err)
		}
		if len(calls) != 1 || calls[0].method != tt.method || calls[0].body != tt.body {
			t.Errorf("calls = %+v, want %s %s", calls, tt.method, tt.body)
		}
	}
}

func handlerCall(t *testing.T, tool string, targets []string, args string) (any, error, bool) {
	t.Helper()
	accessed := false
	red := &redact.Redactor{}
	resolver := secret.NewWith(func(string) string { accessed = true; return testToken }, nil, nil, red)
	resolved := resolvedWith(targets...)
	var op func() (any, error)
	ctx := context.Background()
	raw := json.RawMessage(args)
	switch tool {
	case "name":
		op = func() (any, error) {
			return invokeProfileSet("set bot name", "setMyName", "name", maxBotNameRunes)(ctx, resolved, resolver, red, raw)
		}
	case "description":
		op = func() (any, error) {
			return invokeProfileSet("set bot description", "setMyDescription", "description", maxBotDescriptionRunes)(ctx, resolved, resolver, red, raw)
		}
	case "short":
		op = func() (any, error) {
			return invokeProfileSet("set bot short description", "setMyShortDescription", "short_description", maxBotShortDescriptionRunes)(ctx, resolved, resolver, red, raw)
		}
	default:
		op = func() (any, error) { return invokeProfileGet(ctx, resolved, resolver, red, raw) }
	}
	out, err := op()
	return out, err, accessed
}

func TestProfileToolsRefusedWithoutBotTargetBeforeSecretAccess(t *testing.T) {
	for _, tool := range []string{"name", "description", "short", "get"} {
		_, err, accessed := handlerCall(t, tool, []string{"-100"}, profileArgs(tool, "x"))
		if err == nil || accessed {
			t.Errorf("%s: err = %v, secret accessed = %v", tool, err, accessed)
		}
	}
}

func profileArgs(tool, value string) string {
	switch tool {
	case "name":
		return `{"name":"` + value + `"}`
	case "description":
		return `{"description":"` + value + `"}`
	case "short":
		return `{"short_description":"` + value + `"}`
	}
	return `{}`
}

func TestProfileLimitsAndLanguageRefusedBeforeSecretAccess(t *testing.T) {
	for _, tt := range []struct {
		tool  string
		limit int
	}{{"name", 64}, {"description", 512}, {"short", 120}} {
		if checkRunes("op", "v", strings.Repeat("ä", tt.limit), 0, tt.limit) != nil ||
			checkRunes("op", "v", strings.Repeat("ä", tt.limit+1), 0, tt.limit) == nil ||
			checkRunes("op", "v", "\xff", 0, tt.limit) == nil {
			t.Errorf("%s: rune bounds wrong", tt.tool)
		}
		_, err, accessed := handlerCall(t, tt.tool, []string{"bot"}, profileArgs(tt.tool, strings.Repeat("ä", tt.limit+1)))
		if err == nil || accessed {
			t.Errorf("%s: over limit err = %v accessed = %v", tt.tool, err, accessed)
		}
		for _, args := range []string{
			strings.TrimSuffix(profileArgs(tt.tool, "x"), "}") + `,"language_code":"DE"}`,
			strings.TrimSuffix(profileArgs(tt.tool, "x"), "}") + `,"language_code":"deu"}`,
			`{}`,
		} {
			_, err, accessed := handlerCall(t, tt.tool, []string{"bot"}, args)
			if err == nil || accessed {
				t.Errorf("%s %q: err = %v accessed = %v", tt.tool, args, err, accessed)
			}
		}
	}
	_, err, accessed := handlerCall(t, "get", []string{"bot"}, `{"language_code":"d"}`)
	if err == nil || accessed {
		t.Errorf("get language: err = %v accessed = %v", err, accessed)
	}
}

func TestProfileSettersSingleRequestNoRetry(t *testing.T) {
	for _, status := range []int{500, 502} {
		var calls []recorded
		client, _ := multiClient(t, testToken, profileFake(&calls, status, `{"ok":false,"description":"LEAKEDDESCRIPTION"}`), "bot")
		_, err := client.setProfileText(context.Background(), "set bot name", "setMyName", "name", "x", "")
		if err == nil || len(calls) != 1 || strings.Contains(err.Error(), "LEAKEDDESCRIPTION") ||
			!strings.Contains(err.Error(), "may have taken effect") {
			t.Errorf("status %d: err = %v calls = %d", status, err, len(calls))
		}
	}
	var calls []recorded
	client, _ := multiClient(t, testToken, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls = append(calls, recorded{})
		return nil, errors.New("timeout")
	}), "bot")
	_, err := client.setProfileText(context.Background(), "set bot name", "setMyName", "name", "x", "")
	if err == nil || len(calls) != 1 {
		t.Errorf("transport: err = %v calls = %d", err, len(calls))
	}
}

func TestProfileGetThreeRequestsAndAllowList(t *testing.T) {
	var calls []recorded
	client, _ := multiClient(t, testToken, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		calls = append(calls, recorded{method, string(data)})
		switch method {
		case "getMyName":
			return response(200, `{"ok":true,"result":{"name":"Helfer","EXTRA":"LEAK"}}`), nil
		case "getMyDescription":
			return response(200, `{"ok":true,"result":{"description":"`+strings.Repeat("d", 600)+`"}}`), nil
		}
		return response(200, `{"ok":true,"result":{"short_description":"kurz"}}`), nil
	}), "bot")
	got, err := client.GetProfile(context.Background(), "de")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[0].method != "getMyName" || calls[1].method != "getMyDescription" ||
		calls[2].method != "getMyShortDescription" {
		t.Fatalf("calls = %+v", calls)
	}
	for _, c := range calls {
		if c.body != `{"language_code":"de"}` {
			t.Errorf("body = %s", c.body)
		}
	}
	encoded, _ := json.Marshal(got)
	want := `{"name":"Helfer","description":"` + strings.Repeat("d", 512) + `","short_description":"kurz"}`
	if string(encoded) != want {
		t.Errorf("output = %s", encoded)
	}
	calls = nil
	if _, err := client.GetProfile(context.Background(), ""); err != nil || calls[0].body != `{}` {
		t.Errorf("no language: %v %+v", err, calls)
	}
}

func TestProfileGetFailsAsAWholeWithoutProviderText(t *testing.T) {
	var calls []recorded
	client, _ := multiClient(t, testToken, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		calls = append(calls, recorded{method: method})
		if method == "getMyDescription" {
			return response(400, `{"ok":false,"description":"LEAKEDDESCRIPTION"}`), nil
		}
		return response(200, `{"ok":true,"result":{"name":"n"}}`), nil
	}), "bot")
	got, err := client.GetProfile(context.Background(), "")
	if err == nil || strings.Contains(err.Error(), "LEAKEDDESCRIPTION") || got != (profileOut{}) {
		t.Errorf("got = %+v err = %v", got, err)
	}
	if len(calls) != 2 {
		t.Errorf("calls = %+v", calls)
	}
}

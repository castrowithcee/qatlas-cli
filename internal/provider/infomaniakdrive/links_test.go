package infomaniakdrive

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// passwordCanary stands for a link password; it must never appear in any result or error.
const (
	passwordCanary = "Canary-Link-Pass-7f3a91"
	linkURL        = "https://kdrive.infomaniak.com/app/share/5001/0b1c2d3e-aaaa-bbbb-cccc-1234567890ab"
	expiryText     = "2026-06-01T00:00:00Z"
	expiryUnix     = 1780272000
)

func fixLinkClock(t *testing.T) {
	t.Helper()
	previous := linkNow
	linkNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { linkNow = previous })
}

func linkJSONOf(fileID int64, right, rawURL string) string {
	return fmt.Sprintf(`{"url":%q,"file_id":%d,"right":%q,"valid_until":%d,"created_by":7,"created_at":1735689600,`+
		`"updated_at":1735776000,"capabilities":{"can_edit":false,"can_see_stats":true,"can_see_info":false,`+
		`"can_download":true,"can_comment":false,"can_request_access":false},"access_blocked":false}`,
		rawURL, fileID, right, expiryUnix)
}

func linkArgs(extra string) string {
	return fmt.Sprintf(`{"drive_id":%d,"file_id":%d%s}`, ownDrive, childFileID, extra)
}

func linkPathOf(fileID int64) string {
	return fmt.Sprintf("/2/drive/%d/files/%d/link", ownDrive, fileID)
}

type linkCase struct {
	name, tool, args, method, body, success string
}

func linkCases() []linkCase {
	return []linkCase{
		{"create", linksCreate.ID, linkArgs(fmt.Sprintf(`,"right":"password","password":%q,"valid_until":%q,`+
			`"can_download":true,"can_edit":false`, passwordCanary, expiryText)), http.MethodPost,
			fmt.Sprintf(`{"can_download":true,"can_edit":false,"password":%q,"right":"password","valid_until":%d}`,
				passwordCanary, expiryUnix), envelopeSuccess(linkJSONOf(childFileID, "password", linkURL))},
		{"update", linksUpdate.ID, linkArgs(`,"can_download":false`), http.MethodPut, `{"can_download":false}`,
			envelopeSuccess(`true`)},
		{"delete", linksDelete.ID, linkArgs(""), http.MethodDelete, "", envelopeSuccess(`true`)},
	}
}

func TestLinkDescriptors(t *testing.T) {
	want := map[string]struct {
		effect capability.Effect
		list   bool
	}{linksGet.ID: {capability.EffectRead, false}, linksList.ID: {capability.EffectRead, false},
		linksCreate.ID: {capability.EffectCreate, true}, linksUpdate.ID: {capability.EffectUpdate, true},
		linksDelete.ID: {capability.EffectDelete, true}}
	for _, d := range []capability.Descriptor{linksGet, linksList, linksCreate, linksUpdate, linksDelete} {
		w := want[d.ID]
		r := d.Risk
		if r.Effect != w.effect || d.RequiresToolAllowList != w.list || r.OpenWorld != true ||
			r.DataSensitivity != linkSensitivity || r.DataSensitivity == dataSensitivity {
			t.Errorf("%s = %+v list=%v", d.ID, r, d.RequiresToolAllowList)
		}
		if w.list && r.Confirmation != capability.ConfirmationRequired || !w.list && r.Confirmation != capability.ConfirmationNone {
			t.Errorf("%s confirmation = %s", d.ID, r.Confirmation)
		}
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			// A link URL is access for whoever holds it, so no profile hands out even the reading tools.
			switch id {
			case linksGet.ID, linksList.ID, linksCreate.ID, linksUpdate.ID, linksDelete.ID:
				t.Errorf("profile %s contains %s", profile.ID, id)
			}
		}
	}
}

func TestLinkChangesSendExactlyOneRequest(t *testing.T) {
	fixLinkClock(t)
	for _, tt := range linkCases() {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(200, tt.success), nil
			}), nil)
			result, err := env.invokeConfirmed(tt.tool, "links", tt.args)
			if err != nil {
				t.Fatalf("invoke() = %v", err)
			}
			if len(calls) != 2 || calls[0].path != ownershipPath(ownDrive) {
				t.Fatalf("calls = %+v, want the ownership check then one request", calls)
			}
			got := calls[1]
			if got.method != tt.method || got.path != linkPathOf(childFileID) || len(got.query) != 0 {
				t.Fatalf("call = %+v, want %s %s", got, tt.method, linkPathOf(childFileID))
			}
			if tt.body != "" && !jsonEqual(got.body, tt.body) || tt.body == "" && got.body != "" {
				t.Fatalf("body = %q, want %q", got.body, tt.body)
			}
			var out LinkResult
			if err := json.Unmarshal([]byte(result), &out); err != nil || out.Status != statusDone ||
				out.DriveID != ownDrive || out.FileID != childFileID {
				t.Fatalf("result = %s, %v", result, err)
			}
			if tt.name == "create" && (out.Link == nil || out.Link.URL != linkURL || out.Link.Right != "password" ||
				out.Link.ValidUntil != expiryText) {
				t.Fatalf("result = %s, want the created link", result)
			}
			if tt.name != "create" && out.Link != nil {
				t.Fatalf("result = %s, want no link", result)
			}
			if strings.Contains(result, passwordCanary) {
				t.Fatalf("result = %s shows the password", result)
			}
		})
	}
}

func jsonEqual(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return string(ax) == string(by)
}

func TestLinkChangesNeedConfirmationAndToolList(t *testing.T) {
	for _, tt := range linkCases() {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			_, err := env.invoke(tt.tool, "links", tt.args)
			var needed *application.ConfirmationRequiredError
			if !errors.As(err, &needed) {
				t.Fatalf("err = %v, want confirmation required", err)
			}
			for _, connection := range []string{"linksunlisted", "drive", "readonly", "trash"} {
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

func TestLinkToolsRefuseForeignDrives(t *testing.T) {
	fixLinkClock(t)
	cases := append(linkCases(),
		linkCase{name: "get", tool: linksGet.ID, args: linkArgs("")},
		linkCase{name: "list", tool: linksList.ID, args: fmt.Sprintf(`{"drive_id":%d}`, ownDrive)})
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			foreign := strings.Replace(tt.args, fmt.Sprintf(`"drive_id":%d`, ownDrive),
				fmt.Sprintf(`"drive_id":%d`, foreignDrive), 1)
			_, err := env.invokeConfirmed(tt.tool, "links", foreign)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), fmt.Sprint(foreignDrive)) {
				t.Fatalf("err = %v, want an invalid request without the foreign value", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
			// An allow-list that names a drive of another account is caught by the live check, before the link.
			calls = nil
			env = newEnvironment(t, &calls, withOwnership(foreignDrive, otherAccount, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("request %s reached a drive of another account", r.URL.Path)
				return nil, nil
			}), nil)
			_, err = env.invokeConfirmed(tt.tool, "linksforeign", foreign)
			if !isInvalidRequest(err) || len(calls) != 1 || calls[0].method != http.MethodGet {
				t.Fatalf("err = %v, calls = %+v, want only the ownership check and a refusal", err, calls)
			}
		})
	}
}

func TestLinkToolsValidateArguments(t *testing.T) {
	fixLinkClock(t)
	short := fmt.Sprintf(`"password":%q`, "abc")
	valid := fmt.Sprintf(`"password":%q`, "valid-length-value")
	control := fmt.Sprintf(`"password":%q`, "abcdefgh\x07ij")
	cases := []struct{ name, tool, args string }{
		{"root get", linksGet.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1}`, ownDrive)},
		{"root create", linksCreate.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1,"right":"inherit"}`, ownDrive)},
		{"root update", linksUpdate.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1,"can_edit":true}`, ownDrive)},
		{"root delete", linksDelete.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":1}`, ownDrive)},
		{"zero id", linksDelete.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":0}`, ownDrive)},
		{"negative id", linksGet.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":-4}`, ownDrive)},
		{"string id", linksGet.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":"5/../9"}`, ownDrive)},
		{"huge id", linksGet.ID, fmt.Sprintf(`{"drive_id":%d,"file_id":9999999999999999999}`, ownDrive)},
		{"missing right", linksCreate.ID, linkArgs("")},
		{"unknown right", linksCreate.ID, linkArgs(`,"right":"everyone"`)},
		{"password without right", linksCreate.ID, linkArgs(`,"right":"public",` + valid)},
		{"password right without password", linksCreate.ID, linkArgs(`,"right":"password"`)},
		{"short password", linksCreate.ID, linkArgs(`,"right":"password",` + short)},
		{"control password", linksCreate.ID, linkArgs(`,"right":"password",` + control)},
		{"update password alone", linksUpdate.ID, linkArgs(`,` + valid)},
		{"update empty", linksUpdate.ID, linkArgs("")},
		{"past expiry", linksCreate.ID, linkArgs(`,"right":"inherit","valid_until":"2025-06-01T00:00:00Z"`)},
		{"far expiry", linksCreate.ID, linkArgs(`,"right":"inherit","valid_until":"2040-06-01T00:00:00Z"`)},
		{"bad expiry", linksCreate.ID, linkArgs(`,"right":"inherit","valid_until":"tomorrow at noon please"`)},
		{"null expiry", linksUpdate.ID, linkArgs(`,"valid_until":null`)},
		{"extra field", linksCreate.ID, linkArgs(`,"right":"inherit","method":"DELETE"`)},
		{"extra on get", linksGet.ID, linkArgs(`,` + valid)},
		{"limit low", linksList.ID, fmt.Sprintf(`{"drive_id":%d,"limit":1}`, ownDrive)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}, nil)
			if _, err := env.invokeConfirmed(tt.tool, "links", tt.args); err == nil {
				t.Fatal("a malformed request was accepted")
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

func TestLinkChangeErrorsAndUncertainOutcomesAreNeverRepeated(t *testing.T) {
	fixLinkClock(t)
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
	for _, tt := range linkCases() {
		for _, s := range statuses {
			t.Run(fmt.Sprintf("%s %d", tt.name, s.status), func(t *testing.T) {
				var calls []call
				env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
					return jsonResponse(s.status, `{"result":"error","error":{"description":"`+foreignCanary+" "+passwordCanary+`"}}`), nil
				}), nil)
				_, err := env.invokeConfirmed(tt.tool, "links", tt.args)
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
				_, err := env.invokeConfirmed(tt.tool, "links", tt.args)
				if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(calls) != 2 ||
					strings.Contains(err.Error(), passwordCanary) {
					t.Fatalf("err = %v, calls = %d, want the uncertainty after one request", err, len(calls))
				}
			})
		}
		t.Run(tt.name+" asynchronous", func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(200, `{"result":"asynchronous","data":null}`), nil
			}), nil)
			result, err := env.invokeConfirmed(tt.tool, "links", tt.args)
			var out LinkResult
			if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.Status != statusPending || len(calls) != 2 {
				t.Fatalf("result = %s, %v, calls = %d, want pending", result, err, len(calls))
			}
		})
	}
}

// A created link whose answer cannot be read, names another file, or has an unknown right is an unclear
// outcome: the link may exist.
func TestLinkCreateWithUnusableAnswerReportsUncertainty(t *testing.T) {
	fixLinkClock(t)
	answers := map[string]string{
		"flag":       envelopeSuccess(`true`),
		"other file": envelopeSuccess(linkJSONOf(childFileID+1, "inherit", linkURL)),
		"odd right":  envelopeSuccess(linkJSONOf(childFileID, "everyone", linkURL)),
	}
	for name, answer := range answers {
		t.Run(name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, answer), nil
			}), nil)
			_, err := env.invokeConfirmed(linksCreate.ID, "links", linkArgs(`,"right":"inherit"`))
			if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "may have been applied") ||
				len(calls) != 2 {
				t.Fatalf("err = %v, calls = %d", err, len(calls))
			}
		})
	}
}

// The password is registered before the first request, and nothing it reaches shows it: not the result, which
// a provider answer that echoes it must not change, and not an error.
func TestLinkPasswordNeverAppearsAnywhere(t *testing.T) {
	fixLinkClock(t)
	args := linkArgs(fmt.Sprintf(`,"right":"password","password":%q`, passwordCanary))
	t.Run("registered before the first request", func(t *testing.T) {
		var calls []call
		var env *environment
		env = newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, envelopeSuccess(linkJSONOf(childFileID, "password", linkURL))), nil
		}), nil)
		first := true
		previous := transport
		transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if first {
				first = false
				if env.red.Apply(passwordCanary) == passwordCanary {
					t.Error("the password was not registered before the first request")
				}
			}
			return previous.RoundTrip(r)
		})
		t.Cleanup(func() { transport = previous })
		if _, err := env.invokeConfirmed(linksCreate.ID, "links", args); err != nil {
			t.Fatalf("invoke() = %v", err)
		}
	})
	t.Run("provider echo is dropped", func(t *testing.T) {
		var calls []call
		echoed := strings.Replace(linkJSONOf(childFileID, "password", linkURL), `"right"`,
			fmt.Sprintf(`"password":%q,"right"`, passwordCanary), 1)
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, envelopeSuccess(echoed)), nil
		}), nil)
		result, err := env.invokeConfirmed(linksCreate.ID, "links", args)
		if err != nil || strings.Contains(result, passwordCanary) {
			t.Fatalf("result = %s, %v", result, err)
		}
		// A password inside the URL is masked by the redactor itself.
		calls = nil
		env = newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, envelopeSuccess(linkJSONOf(childFileID, "password", linkURL+"?p="+passwordCanary))), nil
		}), nil)
		result, err = env.invokeConfirmed(linksCreate.ID, "links", args)
		if err != nil || strings.Contains(result, passwordCanary) {
			t.Fatalf("result = %s, %v", result, err)
		}
	})
	t.Run("refusals and failures", func(t *testing.T) {
		var calls []call
		env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
			return jsonResponse(500, passwordCanary), nil
		}), nil)
		for _, input := range []string{
			args,
			linkArgs(fmt.Sprintf(`,"right":"public","password":%q`, passwordCanary)),
			fmt.Sprintf(`{"drive_id":%d,"file_id":1,"right":"password","password":%q}`, ownDrive, passwordCanary),
			fmt.Sprintf(`{"drive_id":%d,"file_id":%d,"right":"password","password":%q}`, foreignDrive, childFileID, passwordCanary),
		} {
			_, err := env.invokeConfirmed(linksCreate.ID, "links", input)
			if err == nil || strings.Contains(err.Error(), passwordCanary) || strings.Contains(env.red.Error(err), passwordCanary) {
				t.Fatalf("err = %v", err)
			}
		}
		if _, err := env.invoke(linksCreate.ID, "links", args); err == nil || strings.Contains(err.Error(), passwordCanary) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestLinksGetReadsOneLinkAndDropsUnsafeValues(t *testing.T) {
	fixLinkClock(t)
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, envelopeSuccess(linkJSONOf(childFileID, "inherit", linkURL))), nil
	}), nil)
	result, err := env.invoke(linksGet.ID, "links", linkArgs(""))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	if len(calls) != 2 || calls[1].method != http.MethodGet || calls[1].path != linkPathOf(childFileID) || calls[1].body != "" {
		t.Fatalf("calls = %+v", calls)
	}
	var out LinkResult
	if err := json.Unmarshal([]byte(result), &out); err != nil || out.Status != "" || out.Link == nil ||
		out.Link.URL != linkURL || out.Link.Right != "inherit" || !out.Link.CanDownload || out.Link.CanEdit ||
		out.Link.ValidUntil != expiryText || out.Link.CreatedAt != "2025-01-01T00:00:00Z" {
		t.Fatalf("result = %s, %v", result, err)
	}

	unsafe := map[string]string{
		"http":        "http://kdrive.infomaniak.com/app/share/1/2",
		"javascript":  "javascript:alert(1)",
		"userinfo":    (&url.URL{Scheme: "https", User: url.UserPassword("user", "pw"), Host: "kdrive.infomaniak.com", Path: "/app/share/1/2"}).String(),
		"schemeless":  "kdrive.infomaniak.com/app/share/1/2",
		"too long":    "https://kdrive.infomaniak.com/" + strings.Repeat("a", 2000),
		"control":     "https://kdrive.infomaniak.com/a\nb",
		"empty":       "",
		"no host":     "https:///path",
		"file scheme": "file:///etc/passwd",
	}
	for name, bad := range unsafe {
		t.Run(name, func(t *testing.T) {
			calls = nil
			env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, envelopeSuccess(linkJSONOf(childFileID, "public", bad))), nil
			}), nil)
			result, err := env.invoke(linksGet.ID, "links", linkArgs(""))
			var out LinkResult
			if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.Link == nil || out.Link.URL != "" ||
				out.Link.Right != "public" {
				t.Fatalf("result = %s, %v, want the link without its URL", result, err)
			}
		})
	}
}

func TestLinksGetFailures(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
		return jsonResponse(404, `{"result":"error"}`), nil
	}), nil)
	if _, err := env.invoke(linksGet.ID, "links", linkArgs("")); classOf(err) != provider.ClassNotFound {
		t.Fatalf("err = %v, want not found for a file without a link", err)
	}
	env = newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"result":"error"}`), nil
	}), nil)
	if _, err := env.invoke(linksGet.ID, "links", linkArgs("")); classOf(err) != provider.ClassPermission {
		t.Fatalf("err = %v, want permission", err)
	}
	for name, answer := range map[string]string{
		"other file": envelopeSuccess(linkJSONOf(childFileID+1, "inherit", linkURL)),
		"odd right":  envelopeSuccess(linkJSONOf(childFileID, "everyone", linkURL)),
		"error":      `{"result":"error"}`,
		"garbage":    `not json`,
	} {
		env = newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, answer), nil
		}), nil)
		if _, err := env.invoke(linksGet.ID, "links", linkArgs("")); classOf(err) != provider.ClassInvalidResponse {
			t.Fatalf("%s: err = %v, want an invalid response", name, err)
		}
	}
}

func TestLinksListPaginatesAndBoundsStrings(t *testing.T) {
	fixLinkClock(t)
	var calls []call
	long := strings.Repeat("n", 5000)
	item := func(id int64, name, link string) string {
		return fmt.Sprintf(`{"id":%d,"name":%q,"type":"file","parent_id":4,"status":"ok","last_modified_at":1735776000%s}`,
			id, name, link)
	}
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"result":"success","data":[`+
			item(7, long, `,"sharelink":`+linkJSONOf(7, "public", linkURL))+`,`+
			item(8, "b.txt", `,"sharelink":`+linkJSONOf(9, "public", linkURL))+`,`+
			item(10, "c.txt", `,"sharelink":`+linkJSONOf(10, "public", "http://plain.example/x"))+`,`+
			item(11, "d.txt", "")+`],"cursor":"next-page","has_more":true}`), nil
	}), nil)
	result, err := env.invoke(linksList.ID, "links", fmt.Sprintf(`{"drive_id":%d,"cursor":"abc","limit":20}`, ownDrive))
	if err != nil {
		t.Fatalf("invoke() = %v", err)
	}
	q := calls[1].query
	if len(calls) != 2 || calls[1].method != http.MethodGet || calls[1].path != fmt.Sprintf("/3/drive/%d/files/links", ownDrive) ||
		q.Get("cursor") != "abc" || q.Get("limit") != "20" || q.Get("with") != "sharelink" || calls[1].body != "" {
		t.Fatalf("calls = %+v", calls)
	}
	var page LinkPage
	if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != 4 || !page.HasMore ||
		page.Cursor != "next-page" || len(page.Entries[0].Name) >= len(long) || page.Entries[0].Link == nil ||
		page.Entries[0].Link.URL != linkURL || page.Entries[1].Link != nil || page.Entries[2].Link == nil ||
		page.Entries[2].Link.URL != "" || page.Entries[3].Link != nil {
		t.Fatalf("page = %s, %v", result, err)
	}

	calls = nil
	if _, err := env.invoke(linksList.ID, "links", fmt.Sprintf(`{"drive_id":%d}`, ownDrive)); err != nil ||
		calls[1].query.Get("limit") != "10" || calls[1].query.Has("cursor") {
		t.Fatalf("default call = %+v, %v", calls, err)
	}
}

func TestLinksListPermissionFailure(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, withOwnership(ownDrive, ownAccount, func(*http.Request) (*http.Response, error) {
		return jsonResponse(403, `{"result":"error"}`), nil
	}), nil)
	if _, err := env.invoke(linksList.ID, "links", fmt.Sprintf(`{"drive_id":%d}`, ownDrive)); classOf(err) != provider.ClassPermission {
		t.Fatalf("err = %v, want permission", err)
	}
}

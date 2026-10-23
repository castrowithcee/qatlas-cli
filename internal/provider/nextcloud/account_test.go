package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	backendCanary  = "backend-canary-Database"
	locationCanary = "/srv/storage-canary/alice"
	phoneCanary    = "+49-canary-phone"
)

var accountJSON = `{"id":"alice","enabled":true,"storageLocation":"` + locationCanary + `","backend":"` + backendCanary + `",
"lastLogin":1700000000000,"displayname":"Alice A","display-name":"Alice A","email":"` + "alice" + "@" + "example.invalid" + `",
"phone":"` + phoneCanary + `","address":"canary street","website":"https://example.org","twitter":"@alice",
"language":"de","locale":"de_DE","backendCapabilities":{"setDisplayName":true,"setPassword":true},"subadmin":["canary-admin"],
"groups":["staff","dev"],"quota":{"free":900,"used":100,"total":1000,"relative":10.5,"quota":1000.5,"path":"` + locationCanary + `"}}`

func TestAccountGetReportsEveryDocumentedFieldButTheStorageLocation(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(200, ocsData(accountJSON)), nil })
	got, err := runAccount(invokeAccountGet, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	out := marshal(got)
	want := `{"id":"alice","display_name":"Alice A","email":"alice@example.invalid","enabled":true,"language":"de","locale":"de_DE",` +
		`"last_login":"2023-11-14T22:13:20Z","groups":["staff","dev"],"subadmin_groups":["canary-admin"],"phone":"` + phoneCanary + `",` +
		`"address":"canary street","website":"https://example.org","twitter":"@alice","backend":"` + backendCanary + `",` +
		`"backend_capabilities":{"set_display_name":true,"set_password":true},` +
		`"quota":{"used":100,"total":1000,"relative":10.5,"free":900}}`
	if out != want {
		t.Errorf("account = %s\nwant      %s", out, want)
	}
	for _, canary := range []string{locationCanary, "display-name", "storageLocation"} {
		if strings.Contains(out, canary) {
			t.Errorf("output carries %q: %s", canary, out)
		}
	}
	raw, _ := json.Marshal(got)
	checkDeclared(t, accountGet, raw, accountGet.OutputSchema)
	if len(*calls) != 1 || (*calls)[0].method != http.MethodGet || !strings.HasSuffix((*calls)[0].url.Path, "/ocs/v2.php/cloud/user") {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestAccountGetWithoutNewMembersKeepsTheOutput(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, ocsData(`{"id":"alice","displayname":"Alice A","language":"de","locale":"de_DE","groups":["staff"],`+
			`"phone":"","website":null,"backendCapabilities":[],"subadmin":[],"lastLogin":0,"quota":{"used":1,"total":-3,"relative":0}}`)), nil
	})
	got, err := runAccount(invokeAccountGet, `{}`)
	want := `{"id":"alice","display_name":"Alice A","language":"de","locale":"de_DE","groups":["staff"],"quota":{"used":1,"total":-3,"relative":0}}`
	if err != nil || marshal(got) != want {
		t.Errorf("account = %s, %v", marshal(got), err)
	}
}

func TestAccountGetCutsAndCaps(t *testing.T) {
	var groups []string
	for i := 0; i < 60; i++ {
		groups = append(groups, `"g`+itoa(i)+`"`)
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, ocsData(`{"id":"alice","displayname":"`+strings.Repeat("n", 500)+`","groups":[`+strings.Join(groups, ",")+`],"quota":[]}`)), nil
	})
	got, err := runAccount(invokeAccountGet, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	account := got.(*Account)
	if len(account.Groups) != maxAccountGroups || len(account.DisplayName) != maxAccountText || !account.Truncated || account.Quota != nil {
		t.Errorf("account = %+v", account)
	}
}

func TestAccountGetErrors(t *testing.T) {
	for name, answer := range map[string]func() (*http.Response, error){
		"302":     func() (*http.Response, error) { return status(302), nil },
		"500":     func() (*http.Response, error) { return status(500), nil },
		"garbage": func() (*http.Response, error) { return ocsResponse(200, ocsData(`"x"`)), nil },
	} {
		serve(t, func(*http.Request) (*http.Response, error) { return answer() })
		_, err := runAccount(invokeAccountGet, `{}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), redirectTarget) ||
			(name == "302") != strings.Contains(err.Error(), "redirect") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAccountAndStatusRefuseWithoutAccountTargetBeforeSecretAccess(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	for name, h := range map[string]capability.Handler{"account": invokeAccountGet, "get": invokeStatusGet,
		"set": invokeStatusSet, "clear": invokeStatusClear} {
		_, err := accountBound(h)(capability.WithConfirmed(context.Background()), reportsConnection(), res, &redact.Redactor{},
			json.RawMessage(`{"type":"dnd"}`))
		if err == nil {
			t.Errorf("%s: no error without the account target", name)
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

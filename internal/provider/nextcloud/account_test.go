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
"displayname":"Alice A","email":"` + "alice" + "@" + "example.invalid" + `","phone":"` + phoneCanary + `","address":"canary street",
"language":"de","locale":"de_DE","backendCapabilities":{"setDisplayName":true},"subadmin":["canary-admin"],
"groups":["staff","dev"],"quota":{"free":900,"used":100,"total":1000,"relative":10.5,"quota":"1000 B","path":"` + locationCanary + `"}}`

func TestAccountGetReportsOnlyAllowedFields(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(200, ocsData(accountJSON)), nil })
	got, err := runAccount(invokeAccountGet, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	account := got.(*Account)
	if account.ID != "alice" || account.DisplayName != "Alice A" || account.Language != "de" || account.Locale != "de_DE" ||
		len(account.Groups) != 2 || account.Quota == nil || *account.Quota != (Quota{100, 1000, 10.5}) || account.Truncated {
		t.Errorf("account = %+v", account)
	}
	out := marshal(got)
	for _, canary := range []string{backendCanary, locationCanary, phoneCanary, "canary", "enabled", "free", "1000 B"} {
		if strings.Contains(out, canary) {
			t.Errorf("output carries %q: %s", canary, out)
		}
	}
	if len(*calls) != 1 || (*calls)[0].method != http.MethodGet || !strings.HasSuffix((*calls)[0].url.Path, "/ocs/v2.php/cloud/user") {
		t.Errorf("calls = %+v", *calls)
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

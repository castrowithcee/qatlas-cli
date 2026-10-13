package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	remoteTokenCanary = "remote-share-token-canary-nextcloud-5a1e"
	remotePathCanary  = "remote-path-canary-nextcloud-91c2"
	remoteBase        = "/ocs/v2.php/apps/files_sharing/api/v1/remote_shares"
)

func remoteJSON(id, remote, mountpoint string) string {
	return `{"id":"` + id + `","parent":null,"share_type":0,"remote":"` + remote + `","remote_id":"33",` +
		`"share_token":"` + remoteTokenCanary + `","password":"` + remoteTokenCanary + `","refresh_token":"` + remoteTokenCanary +
		`","name":"/Projects","owner":"carol","user":"alice","mountpoint":"` + mountpoint + `","accepted":1}`
}

func remoteTool(tool capability.Handler, resolvedAccount bool, raw string) (any, error) {
	red := &redact.Redactor{}
	resolved := reportsConnection()
	if resolvedAccount {
		resolved = accountConnection()
	}
	return accountBound(tool)(capability.WithConfirmed(context.Background()), resolved, resolver(red), red, json.RawMessage(raw))
}

func TestRemoteSharesRefuseWithoutAccountTargetBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	for _, tool := range []capability.Handler{invokeRemoteSharesList, invokeRemoteSharesGet, invokeRemoteSharesRemove} {
		_, err := accountBound(tool)(capability.WithConfirmed(context.Background()), reportsConnection(), res,
			&redact.Redactor{}, json.RawMessage(`{"share_id":"5"}`))
		if err == nil {
			t.Error("accepted a connection without an account target")
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func TestRemoteSharesRefuseBadIDsLocally(t *testing.T) {
	refuse(t)
	touched := false
	res := secret.NewWith(func(string) string { touched = true; return "x" }, nil, nil, &redact.Redactor{})
	for _, id := range []string{"", "a", "1/2", "..", "5?x=1", "12345678901234567890", "-1"} {
		for _, tool := range []capability.Handler{invokeRemoteSharesGet, invokeRemoteSharesRemove} {
			raw, _ := json.Marshal(obj{"share_id": id})
			_, err := accountBound(tool)(capability.WithConfirmed(context.Background()), accountConnection(), res,
				&redact.Redactor{}, raw)
			if err == nil {
				t.Errorf("accepted %q", id)
			}
		}
	}
	if touched {
		t.Error("a secret was read")
	}
}

func TestRemoteSharesListReportsHostOnlyAndNoSecrets(t *testing.T) {
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, ocsData(`[`+
			remoteJSON("5", "https://192.0.2.7:8443/"+remotePathCanary+"?t=1", "/Shared/Projects")+`,`+
			remoteJSON("6", "https://tok:pw@192.0.2.8/"+remotePathCanary, "/../escape")+`,`+
			remoteJSON("x", "https://192.0.2.9", "/Shared/x")+`,`+
			remoteJSON("7", "192.0.2.10", "/Shared/Plain")+`]`)), nil
	})
	got, err := remoteTool(invokeRemoteSharesList, true, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	result := got.(*RemoteSharesResult)
	if result.Count != 3 || result.Truncated || result.Shares[0] != (RemoteShare{ID: "5", Owner: "carol", Host: "192.0.2.7:8443",
		Name: "Projects", Mountpoint: "Shared/Projects"}) {
		t.Fatalf("result = %+v", result)
	}
	if result.Shares[1].Host != "192.0.2.8" || result.Shares[1].Mountpoint != "" || result.Shares[2].Host != "192.0.2.10" {
		t.Errorf("shares = %+v", result.Shares)
	}
	encoded, _ := json.Marshal(result)
	for _, secretValue := range []string{remoteTokenCanary, remotePathCanary, "tok", "pw@", "https://", "?t=1", textCanary} {
		if strings.Contains(string(encoded), secretValue) {
			t.Errorf("output contains %q: %s", secretValue, encoded)
		}
	}
	if len(*calls) != 1 || (*calls)[0].method != http.MethodGet || (*calls)[0].url.Path != remoteBase ||
		(*calls)[0].url.Query().Get("format") != "json" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestRemoteSharesListCapsEntriesAndStrings(t *testing.T) {
	var items []string
	for i := 1; i <= maxShareEntries+3; i++ {
		items = append(items, remoteJSON(fmt.Sprint(i), "https://192.0.2.7", "/Shared/a"))
	}
	long := strings.Repeat("ä", maxValueLength)
	items[0] = strings.Replace(items[0], `"owner":"carol"`, `"owner":"`+long+`"`, 1)
	serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(200, ocsData(`[`+strings.Join(items, ",")+`]`)), nil
	})
	got, err := remoteTool(invokeRemoteSharesList, true, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	result := got.(*RemoteSharesResult)
	if result.Count != maxShareEntries || !result.Truncated || !result.Shares[0].Truncated ||
		len(result.Shares[0].Owner) > maxValueLength || strings.ContainsRune(result.Shares[0].Owner, '�') || result.Shares[1].Truncated {
		t.Errorf("count = %d, truncated = %v, first = %d bytes/%v", result.Count, result.Truncated,
			len(result.Shares[0].Owner), result.Shares[0].Truncated)
	}
}

func TestRemoteSharesListWithoutFederationIsClear(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return ocsResponse(404, `{"ocs":{"meta":{"status":"failure","statuscode":404,"message":"`+textCanary+`"},"data":[]}}`), nil
	})
	_, err := remoteTool(invokeRemoteSharesList, true, `{}`)
	if err == nil || !strings.Contains(err.Error(), messageFederationOff) || strings.Contains(err.Error(), textCanary) {
		t.Errorf("err = %v", err)
	}
}

func TestRemoteSharesGetReadsOneAndHidesForeignOrUnknown(t *testing.T) {
	calls := serve(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case remoteBase + "/5":
			return ocsResponse(200, ocsData(remoteJSON("5", "https://192.0.2.7/x", "/Shared/Projects"))), nil
		case remoteBase + "/6":
			return ocsResponse(200, ocsData(remoteJSON("99", "https://198.51.100.1", "/Shared/Other"))), nil
		}
		return ocsResponse(404, ocsData(`[]`)), nil
	})
	got, err := remoteTool(invokeRemoteSharesGet, true, `{"share_id":"5"}`)
	if err != nil || got.(*RemoteShare).Host != "192.0.2.7" || got.(*RemoteShare).ID != "5" {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
	for _, id := range []string{"6", "8"} {
		_, err = remoteTool(invokeRemoteSharesGet, true, `{"share_id":"`+id+`"}`)
		if err == nil || classOf(err) != provider.ClassNotFound || !strings.Contains(err.Error(), messageRemoteShareNotFound) ||
			strings.Contains(err.Error(), "198.51.100.1") {
			t.Errorf("id %s: %v", id, err)
		}
	}
	if len(*calls) != 3 {
		t.Errorf("calls = %d", len(*calls))
	}
}

func removeServer(t *testing.T, change func(*http.Request) (*http.Response, error)) *[]call {
	return serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return ocsResponse(200, ocsData(remoteJSON("5", "https://192.0.2.7/x", "/Shared/Projects"))), nil
		}
		return change(r)
	})
}

func TestRemoteSharesRemoveSendsOneDeleteAfterOneRead(t *testing.T) {
	calls := removeServer(t, func(*http.Request) (*http.Response, error) { return ocsResponse(200, ocsData(`[]`)), nil })
	got, err := remoteTool(invokeRemoteSharesRemove, true, `{"share_id":"5"}`)
	if err != nil {
		t.Fatal(err)
	}
	if m := got.(map[string]any); m["removed"] != true || m["id"] != "5" || m["host"] != "192.0.2.7" {
		t.Errorf("got = %+v", m)
	}
	if len(*calls) != 2 || (*calls)[0].method != http.MethodGet || (*calls)[1].method != http.MethodDelete ||
		(*calls)[1].url.Path != remoteBase+"/5" {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestRemoteSharesRemoveNeverRepeatsAfterAnUnclearOutcome(t *testing.T) {
	cases := map[string]func(*http.Request) (*http.Response, error){
		"5xx":        func(*http.Request) (*http.Response, error) { return ocsResponse(500, `oops`), nil },
		"timeout":    func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"abort":      func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") },
		"unreadable": func(*http.Request) (*http.Response, error) { return ocsResponse(200, `not json`), nil },
	}
	for name, change := range cases {
		calls := removeServer(t, change)
		_, err := remoteTool(invokeRemoteSharesRemove, true, `{"share_id":"5"}`)
		deletes := 0
		for _, c := range *calls {
			if c.method == http.MethodDelete {
				deletes++
			}
		}
		if err == nil || !strings.Contains(err.Error(), uncertainRemoteShareRemove) || deletes != 1 || len(*calls) != 2 {
			t.Errorf("%s: err = %v, calls = %d, deletes = %d", name, err, len(*calls), deletes)
		}
	}
}

func TestRemoteSharesRemoveRefusals(t *testing.T) {
	removeServer(t, func(*http.Request) (*http.Response, error) { return ocsResponse(403, `{}`), nil })
	_, err := remoteTool(invokeRemoteSharesRemove, true, `{"share_id":"5"}`)
	if err == nil || !strings.Contains(err.Error(), messageRemoteShareRefused) || strings.Contains(err.Error(), "may have been") {
		t.Errorf("403: %v", err)
	}
	calls := serve(t, func(*http.Request) (*http.Response, error) { return ocsResponse(404, `{}`), nil })
	_, err = remoteTool(invokeRemoteSharesRemove, true, `{"share_id":"5"}`)
	if err == nil || classOf(err) != provider.ClassNotFound || len(*calls) != 1 {
		t.Errorf("404: %v, calls = %d", err, len(*calls))
	}
}

func TestRemoteSharesRedirectsAreNotFollowed(t *testing.T) {
	for _, status := range []int{301, 302, 307} {
		calls := serve(t, func(*http.Request) (*http.Response, error) {
			response := ocsResponse(status, ``)
			response.Header.Set("Location", "https://evil.invalid/"+remotePathCanary)
			return response, nil
		})
		for name, tool := range map[string]capability.Handler{"list": invokeRemoteSharesList, "get": invokeRemoteSharesGet,
			"remove": invokeRemoteSharesRemove} {
			args := `{"share_id":"5"}`
			if name == "list" {
				args = `{}`
			}
			_, err := remoteTool(tool, true, args)
			if err == nil || !strings.Contains(err.Error(), messageRedirect) || strings.Contains(err.Error(), "evil") ||
				strings.Contains(err.Error(), remotePathCanary) {
				t.Errorf("%s %d: %v", name, status, err)
			}
		}
		for _, c := range *calls {
			if c.method == http.MethodDelete {
				t.Errorf("a DELETE followed a redirected read")
			}
		}
	}
}

func TestRemoteSharesDescriptors(t *testing.T) {
	reg := registry(t)
	for _, id := range []string{remoteSharesList.ID, remoteSharesGet.ID, remoteSharesRemove.ID} {
		d, _, ok := reg.Lookup(id)
		if !ok || d.Group != groupShares {
			t.Fatalf("%s = %+v", id, d)
		}
	}
	r := remoteSharesRemove
	if r.Risk.Effect != capability.EffectDelete || !r.Risk.OpenWorld || r.Risk.Confirmation != capability.ConfirmationRequired ||
		r.Risk.Idempotency == "" || r.Risk.DataSensitivity == "" || !r.RequiresToolAllowList {
		t.Errorf("remove = %+v", r.Risk)
	}
	for _, d := range []capability.Descriptor{remoteSharesList, remoteSharesGet} {
		if d.Risk.Effect != capability.EffectRead || d.RequiresToolAllowList {
			t.Errorf("%s = %+v", d.ID, d.Risk)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, p := range metadata.Profiles {
		for _, id := range p.Tools {
			if strings.Contains(id, "remoteshares") {
				t.Errorf("profile %s contains %s", p.ID, id)
			}
		}
	}
}

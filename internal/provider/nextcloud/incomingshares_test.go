package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	internalPending = `[
	{"id":"11","share_type":0,"uid_owner":"bob","uid_initiator":"bob","file_target":"/Plan.odt","path":"/Plan.odt","item_type":"file","stime":1760000000},
	{"id":"12","share_type":1,"uid_owner":"carol","uid_initiator":"carol","file_target":"/Docs","item_type":"folder","stime":1760000100},
	{"id":"13","share_type":3,"uid_owner":"carol","uid_initiator":"carol","file_target":"/Link","item_type":"folder","stime":1760000100},
	{"id":"14","share_type":0,"uid_owner":"alice","uid_initiator":"alice","file_target":"/Mine","item_type":"file","stime":1760000100}]`
	remotePendingJSON = `[{"id":"21","name":"/Remote.pdf","owner":"dave","remote":"https://far.example.org:8443/path?x=1","type":"file","mtime":1700000000,"accepted":0},{"id":"22","name":"/Odd","owner":"erin","remote":"https://user:secret@192.0.2.1","type":"file"}]`
)

func incomingServer(t *testing.T, internal, remote string, change func(*http.Request) (*http.Response, error)) *[]call {
	t.Helper()
	return serve(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/shares/pending"):
			return ocsResponse(200, ocsData(internal)), nil
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/remote_shares/pending"):
			return ocsResponse(200, ocsData(remote)), nil
		}
		return change(r)
	})
}

func incomingOK(*http.Request) (*http.Response, error) { return ocsResponse(200, ocsData(`[]`)), nil }

func runIncoming(fn func(context.Context, *redact.Redactor, json.RawMessage) (any, error), args string) (any, error) {
	return fn(capability.WithConfirmed(context.Background()), &redact.Redactor{}, json.RawMessage(args))
}

func incomingTool(invoke capability.Handler) func(context.Context, *redact.Redactor, json.RawMessage) (any, error) {
	return func(ctx context.Context, red *redact.Redactor, raw json.RawMessage) (any, error) {
		return accountBound(invoke)(ctx, accountConnection(), resolver(red), red, raw)
	}
}

var (
	pendingTool = incomingTool(invokeIncomingPending)
	acceptTool  = incomingTool(invokeIncomingAccept)
	declineTool = incomingTool(invokeIncomingDecline)
)

func TestIncomingPendingReportsOriginAndOnlyAPIBackedTimes(t *testing.T) {
	calls := incomingServer(t, internalPending, remotePendingJSON, incomingOK)
	got, err := runIncoming(pendingTool, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*incomingResult)
	// 13 is a link share and 14 the identity's own share: neither is an offer to the identity.
	if list.Count != 4 || list.Truncated {
		t.Fatalf("list = %+v", list)
	}
	want := []incomingShare{
		{ID: "11", Origin: "internal", Sender: "bob", Name: "Plan.odt", Type: "file", Time: "2025-10-09T08:53:20Z"},
		{ID: "12", Origin: "internal", Sender: "carol", Name: "Docs", Type: "folder", Time: "2025-10-09T08:55:00Z"},
		{ID: "21", Origin: "federated", Sender: "dave", Name: "Remote.pdf", Type: "file", ServerHost: "far.example.org"},
		{ID: "22", Origin: "federated", Sender: "erin", Name: "Odd", Type: "file"},
	}
	for i, share := range list.Shares {
		if share != want[i] {
			t.Errorf("share %d = %+v, want %+v", i, share, want[i])
		}
	}
	if len(*calls) != 2 || (*calls)[0].method != http.MethodGet || (*calls)[1].method != http.MethodGet ||
		!strings.HasSuffix((*calls)[0].url.Path, "/shares/pending") || !strings.HasSuffix((*calls)[1].url.Path, "/remote_shares/pending") {
		t.Errorf("calls = %+v", *calls)
	}
	raw, _ := json.Marshal(list)
	for _, forbidden := range []string{"status", "secret", "8443", "mtime", "1700000000"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("result contains %q: %s", forbidden, raw)
		}
	}
}

func TestIncomingPendingKeepsEarlierDeclinedInternalOffers(t *testing.T) {
	incomingServer(t, `[{"id":"11","share_type":0,"uid_owner":"bob","file_target":"/a.txt","item_type":"file","stime":1760000000,"status":2}]`, `[]`, incomingOK)
	got, err := runIncoming(pendingTool, `{}`)
	if err != nil || got.(*incomingResult).Count != 1 {
		t.Fatalf("got = %+v, %v", got, err)
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "status") {
		t.Errorf("the list reports a status: %s", raw)
	}
}

func TestIncomingPendingIsBoundedAndSaysSo(t *testing.T) {
	var items []string
	for i := 0; i < maxShareEntries+5; i++ {
		items = append(items, `{"id":"`+itoa(i)+`","share_type":0,"uid_owner":"bob","file_target":"/`+strings.Repeat("n", 2000)+`","item_type":"file"}`)
	}
	incomingServer(t, "["+strings.Join(items, ",")+"]", remotePendingJSON, incomingOK)
	got, err := runIncoming(pendingTool, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	list := got.(*incomingResult)
	if list.Count != maxShareEntries || !list.Truncated || !list.Shares[0].Truncated || len(list.Shares[0].Name) > maxValueLength {
		t.Errorf("count = %d truncated = %v first = %+v", list.Count, list.Truncated, list.Shares[0].Truncated)
	}
}

func TestIncomingToolsRefuseWithoutAccountTargetBeforeSecretsAndIO(t *testing.T) {
	refuse(t)
	reads := 0
	red := &redact.Redactor{}
	secrets := secret.NewWith(func(string) string { reads++; return "x" }, nil, nil, red)
	for name, fn := range map[string]capability.Handler{
		"pending": accountBound(invokeIncomingPending), "accept": accountBound(invokeIncomingAccept), "decline": accountBound(invokeIncomingDecline),
	} {
		_, err := fn(capability.WithConfirmed(context.Background()), reportsConnection(), secrets, red, json.RawMessage(`{"share_id":"11","origin":"internal"}`))
		if err == nil || reads != 0 {
			t.Errorf("%s: err = %v, secret reads = %d", name, err, reads)
		}
	}
}

func TestIncomingAcceptSendsOnePostAfterOnePreCheck(t *testing.T) {
	calls := incomingServer(t, internalPending, remotePendingJSON, incomingOK)
	got, err := runIncoming(acceptTool, `{"share_id":"11"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res := got.(map[string]any); res["id"] != "11" || res["origin"] != "internal" || res["changed"] != true {
		t.Errorf("result = %+v", res)
	}
	if len(*calls) != 2 || (*calls)[0].method != http.MethodGet || (*calls)[1].method != http.MethodPost ||
		!strings.HasSuffix((*calls)[1].url.Path, "/shares/pending/11") {
		t.Errorf("calls = %+v", *calls)
	}
}

func TestIncomingAcceptRefusesFederatedAndUnknownIDsBeforeAnyChange(t *testing.T) {
	calls := incomingServer(t, internalPending, remotePendingJSON, incomingOK)
	for _, args := range []string{`{"share_id":"21"}`, `{"share_id":"13"}`, `{"share_id":"14"}`, `{"share_id":"999"}`} {
		_, err := runIncoming(acceptTool, args)
		if err == nil || strings.Contains(err.Error(), "dave") || strings.Contains(err.Error(), "far.example.org") {
			t.Errorf("%s: err = %v", args, err)
		}
	}
	for _, c := range *calls {
		if c.method != http.MethodGet {
			t.Errorf("a change was sent: %+v", c)
		}
	}
	for _, args := range []string{`{"share_id":"../1"}`, `{"share_id":"1a"}`, `{"share_id":""}`, `{"share_id":"11","origin":"federated"}`} {
		calls := incomingServer(t, internalPending, remotePendingJSON, incomingOK)
		if args != `{"share_id":"11","origin":"federated"}` {
			if _, err := runIncoming(acceptTool, args); err == nil || len(*calls) != 0 {
				t.Errorf("%s: err = %v, calls = %d", args, err, len(*calls))
			}
		}
	}
}

func TestIncomingDeclineUsesTheRouteOfTheOrigin(t *testing.T) {
	for _, tc := range []struct{ args, method, suffix string }{
		{`{"share_id":"11","origin":"internal"}`, http.MethodDelete, "/shares/11"},
		{`{"share_id":"21","origin":"federated"}`, http.MethodDelete, "/remote_shares/pending/21"},
	} {
		calls := incomingServer(t, internalPending, remotePendingJSON, incomingOK)
		if _, err := runIncoming(declineTool, tc.args); err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 2 || (*calls)[1].method != tc.method || !strings.HasSuffix((*calls)[1].url.Path, tc.suffix) || (*calls)[1].body != "" {
			t.Errorf("%s: calls = %+v", tc.args, *calls)
		}
	}
	// An ID of the other origin is no pending share of the named one.
	calls := incomingServer(t, internalPending, remotePendingJSON, incomingOK)
	for _, args := range []string{`{"share_id":"21","origin":"internal"}`, `{"share_id":"11","origin":"federated"}`, `{"share_id":"11","origin":"link"}`, `{"share_id":"11"}`} {
		if _, err := runIncoming(declineTool, args); err == nil {
			t.Errorf("%s was accepted", args)
		}
	}
	for _, c := range *calls {
		if c.method != http.MethodGet {
			t.Errorf("a change was sent: %+v", c)
		}
	}
}

func TestIncomingChangesDoNotRepeatAfterAnUnclearOutcome(t *testing.T) {
	unclear := map[string]func() (*http.Response, error){
		"500":     func() (*http.Response, error) { return status(500), nil },
		"503":     func() (*http.Response, error) { return status(503), nil },
		"reset":   func() (*http.Response, error) { return nil, errors.New("connection reset by peer") },
		"timeout": func() (*http.Response, error) { return nil, timeoutError{} },
		"garbage": func() (*http.Response, error) { return ocsResponse(200, "not json"), nil },
	}
	for name, answer := range unclear {
		for _, tool := range []struct {
			name string
			fn   func(context.Context, *redact.Redactor, json.RawMessage) (any, error)
			args string
		}{{"accept", acceptTool, `{"share_id":"11"}`}, {"decline", declineTool, `{"share_id":"21","origin":"federated"}`}} {
			calls := incomingServer(t, internalPending, remotePendingJSON, func(*http.Request) (*http.Response, error) { return answer() })
			_, err := runIncoming(tool.fn, tool.args)
			if err == nil || !strings.Contains(err.Error(), "may have changed") || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s %s: err = %v", tool.name, name, err)
			}
			changes := 0
			for _, c := range *calls {
				if c.method != http.MethodGet {
					changes++
				}
			}
			if changes != 1 {
				t.Errorf("%s %s: %d changes sent", tool.name, name, changes)
			}
		}
	}
}

func TestIncomingRedirectIsAClearErrorWithoutTarget(t *testing.T) {
	for _, code := range []int{301, 302, 307, 308} {
		redirect := func(*http.Request) (*http.Response, error) {
			response := status(code)
			response.Header.Set("Location", redirectTarget)
			return response, nil
		}
		incomingServer(t, internalPending, remotePendingJSON, redirect)
		_, err := runIncoming(acceptTool, `{"share_id":"11"}`)
		if err == nil || strings.Contains(err.Error(), "may have changed") || strings.Contains(err.Error(), "outside-canary") ||
			!strings.Contains(err.Error(), "redirect") {
			t.Errorf("%d: err = %v", code, err)
		}
		serve(t, func(*http.Request) (*http.Response, error) {
			response := status(code)
			response.Header.Set("Location", redirectTarget)
			return response, nil
		})
		if _, err := runIncoming(pendingTool, `{}`); err == nil || strings.Contains(err.Error(), "outside-canary") || !strings.Contains(err.Error(), "redirect") {
			t.Errorf("list %d: err = %v", code, err)
		}
	}
}

func TestIncomingDescriptorsFollowTheContract(t *testing.T) {
	for _, d := range []capability.Descriptor{incomingPending, incomingAccept, incomingDecline} {
		if d.Group != groupShares {
			t.Errorf("%s group = %s", d.ID, d.Group)
		}
	}
	for _, d := range []capability.Descriptor{incomingAccept, incomingDecline} {
		r := d.Risk
		if r.Confirmation != capability.ConfirmationRequired || !r.OpenWorld || r.DataSensitivity == "" || r.Idempotency == "" {
			t.Errorf("%s risk = %+v", d.ID, r)
		}
	}
	if incomingPending.Risk.Effect != capability.EffectRead || incomingAccept.Risk.Effect != capability.EffectUpdate ||
		incomingDecline.Risk.Effect != capability.EffectDelete {
		t.Error("effects differ from the contract")
	}
	if incomingPending.RequiresToolAllowList || incomingAccept.RequiresToolAllowList || !incomingDecline.RequiresToolAllowList {
		t.Error("only decline needs a tools list")
	}
}

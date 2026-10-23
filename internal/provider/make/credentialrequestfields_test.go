package makeapi

import (
	"strings"
	"testing"
)

const richCredRequest = `{"id":"` + credRequestID + `","teamId":1001,"organizationId":501,"userId":3,` +
	`"name":"Req","description":"d","externalProviderId":9007199254740993,"makeProviderId":7,` +
	`"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-02T00:00:00Z","expiresAt":"2026-02-01T00:00:00Z",` +
	`"status":"pending","shouldSendEmail":false,"emailSentAt":"2026-01-01T01:00:00Z",` +
	`"appNames":["google-sheets","sl\u0007ack"],"publicUri":"` + credLink + `",` +
	`"makeProvider":{"id":7,"name":"Pat","email":"pat@example.test"}}`

func TestCredentialRequestReadsReportEveryDocumentedField(t *testing.T) {
	h := newCredEnv(t, ownTeam)
	h.list = `{"requests":[` + richCredRequest + `]}`
	h.one = richCredRequest
	for _, c := range []struct {
		tool, args string
		minimum    int
		appNames   bool
	}{
		{credentialRequestsList.ID, `{}`, 17, true},
		{credentialRequestsGet.ID, `{"request_id":"` + credRequestID + `"}`, 15, false},
	} {
		h.calls = nil
		got, err := h.invoke(c.tool, "open", c.args)
		if err != nil {
			t.Fatal(err)
		}
		if seen := assertOutputDeclared(t, c.tool, []byte(got)); seen < c.minimum {
			t.Errorf("%s: checked %d members: %s", c.tool, seen, got)
		}
		for _, want := range []string{`"user_id":3`, `"external_provider_id":9007199254740993`,
			`"make_provider_id":7`, `"should_send_email":false`, `"email_sent_at":"2026-01-01T01:00:00Z"`,
			`"provider_email":"pat@example.test"`, `"provider_id":7`} {
			if !strings.Contains(got, want) {
				t.Errorf("%s lacks %s: %s", c.tool, want, got)
			}
		}
		if has := strings.Contains(got, `"app_names":["google-sheets","sl ack"]`); has != c.appNames {
			t.Errorf("%s app_names present = %v: %s", c.tool, has, got)
		}
		if strings.Contains(got, credLink) || strings.Contains(got, "publicUri") {
			t.Errorf("%s leaked the link: %s", c.tool, got)
		}
		cols := strings.Join(h.calls[len(h.calls)-1].query["cols[]"], ",")
		if strings.Contains(cols, "appNames") != c.appNames {
			t.Errorf("%s cols = %s", c.tool, cols)
		}
	}
}

func TestCredentialRequestCreateKeepsItsSummary(t *testing.T) {
	h := newCredEnv(t, ownTeam)
	got, err := h.confirmed(credentialRequestsCreate.ID, "open", credCreateArgs)
	if err != nil {
		t.Fatal(err)
	}
	for _, added := range []string{"provider_email", credEmailCanary, "make_provider_id", "user_id"} {
		if strings.Contains(got, added) {
			t.Fatalf("create reports %s: %s", added, got)
		}
	}
}

func TestCredentialRequestListFiltersReachTheRequest(t *testing.T) {
	h := newCredEnv(t, ownTeam)
	if _, err := h.invoke(credentialRequestsList.ID, "open", `{"user_id":3,"make_provider_id":7}`); err != nil {
		t.Fatal(err)
	}
	if q := h.calls[0].query; q.Get("userId") != "3" || q.Get("makeProviderId") != "7" {
		t.Fatalf("query = %v", q)
	}
	h.calls = nil
	for _, args := range []string{`{"user_id":0}`, `{"make_provider_id":-1}`, `{"user_id":"3"}`, `{"user_id":1.5}`} {
		if _, err := h.invoke(credentialRequestsList.ID, "open", args); err == nil {
			t.Errorf("%s accepted", args)
		}
	}
	if len(h.calls) != 0 {
		t.Fatalf("calls = %+v, want none", h.calls)
	}
}

func TestCredentialRequestAppNamesAreBounded(t *testing.T) {
	h := newCredEnv(t, ownTeam)
	names := make([]string, maxRequestAppNames+3)
	for i := range names {
		names[i] = `"` + strings.Repeat("a", 500) + `"`
	}
	h.list = `{"requests":[{"id":"` + credRequestID + `","teamId":1001,"appNames":[` + strings.Join(names, ",") + `]}]}`
	got, err := h.invoke(credentialRequestsList.ID, "open", `{}`)
	if err != nil || !strings.Contains(got, `"app_names_truncated":true`) ||
		strings.Count(got, "aaaa") > maxRequestAppNames*maxCredentialAppNameLength/4+1 ||
		len(got) > maxRequestAppNames*(maxCredentialAppNameLength+4)+400 {
		t.Fatalf("len = %d, %v", len(got), err)
	}
}

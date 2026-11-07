package aws

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/endpoints"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/httpbind"
)

// No restJson1 tool exists yet; these specs are what the generator will write for one, on made-up
// operations, so the protocol switch of the handler is proven before a service package uses it.

func intShape() *httpbind.Shape { return &httpbind.Shape{Kind: httpbind.KindInteger} }

// restRegionsSpec is a global Account style list: the account in the body, a NextToken pager with a page
// size, and the account-argument check.
func restRegionsSpec() toolSpec {
	str := &httpbind.Shape{Kind: httpbind.KindString}
	return toolSpec{
		descriptor: capability.Descriptor{ID: "aws.account.region.list", Group: "sts",
			Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe}},
		service: endpoints.Account, op: "list regions",
		rest: &httpbind.Operation{
			Method: "POST", URI: "/listRegions",
			Input: &httpbind.Shape{Kind: httpbind.KindStruct, Fields: []httpbind.Field{
				{Key: "account_id", Name: "AccountId", Shape: str},
				{Key: "~NextToken", Name: "NextToken", Shape: str},
				{Key: "~MaxResults", Name: "MaxResults", Shape: intShape()}}},
			Output: &httpbind.Shape{Kind: httpbind.KindStruct, Fields: []httpbind.Field{
				{Key: "regions", Name: "Regions", Shape: &httpbind.Shape{Kind: httpbind.KindList, Elem: str}},
				{Key: "~NextToken", Name: "NextToken", Shape: str}}},
		},
		pager:       &httpbind.Pager{TokenIn: "NextToken", TokenOut: "~NextToken", SizeParam: "MaxResults", MaxSize: 50},
		targetCheck: "account-argument", targetArgs: []string{"account_id"}, policyCheck: "none",
	}
}

func regionsAnswer(names, next string) reply {
	body := `{"Regions":["` + strings.ReplaceAll(names, ",", `","`) + `"]`
	if next != "" {
		body += `,"NextToken":"` + next + `"`
	}
	return ok(body + "}")
}

func restRequests(f *fakeAWS) []sent {
	var out []sent
	for _, r := range f.requests {
		if r.action == "" {
			out = append(out, r)
		}
	}
	return out
}

// A restJson1 spec runs through sendRest after the account check: the signed JSON request goes to the fixed
// global endpoint, the paging members are bound by the model's places, and the cursor works as in awsQuery.
func TestRestToolPagesAndChecksTheAccountFirst(t *testing.T) {
	f := direct(func(r sent, _ int) reply {
		var body map[string]any
		_ = json.Unmarshal([]byte(r.body), &body)
		switch body["NextToken"] {
		case nil:
			return regionsAnswer("r1", "t1")
		case "t1":
			return regionsAnswer("r2", "t2")
		}
		return regionsAnswer("r3", "")
	})
	install(t, f)
	s := restRegionsSpec()
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	var names []any
	cursor := ""
	for page := 0; page < 3; page++ {
		args := `{"page_size":2}`
		if cursor != "" {
			args = `{"page_size":2,"cursor":"` + cursor + `"}`
		}
		got, err := s.call(args)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		names = append(names, got["regions"].([]any)...)
		next, _ := got["next_cursor"].(string)
		if _, leaked := got["~NextToken"]; leaked || got["has_more"] != (page < 2) || (next != "") != (page < 2) || strings.Contains(next, "t1") {
			t.Errorf("page %d result = %v", page, got)
		}
		if _, regional := got["region"]; regional {
			t.Errorf("a global service reported a region: %v", got)
		}
		cursor = next
	}
	if !slices.Equal(names, []any{"r1", "r2", "r3"}) {
		t.Errorf("names = %v", names)
	}
	calls := restRequests(f)
	if len(calls) != 3 || f.count("GetCallerIdentity") != 1 {
		t.Fatalf("rest calls = %d, identity calls = %d", len(calls), f.count("GetCallerIdentity"))
	}
	for _, r := range calls {
		if r.method != "POST" || r.path != "/listRegions" || r.host != "account.us-east-1.amazonaws.com" ||
			r.header.Get("Content-Type") != "application/json" || !strings.Contains(r.body, `"MaxResults":2`) {
			t.Errorf("request = %+v", r)
		}
	}
	// A refused page never leaves: a cursor of other arguments, a tampered one, a size above the maximum.
	first, _ := s.call(`{}`)
	issued := first["next_cursor"].(string)
	before := len(f.requests)
	for _, args := range []string{`{"account_id":"` + testAccount + `","cursor":"` + issued + `"}`,
		`{"cursor":"` + issued[:len(issued)-1] + `x"}`, `{"page_size":51}`} {
		if _, err := s.call(args); !isInvalidRequest(err) {
			t.Errorf("%s: %v, want an invalid request", args, err)
		}
	}
	if len(f.requests) != before {
		t.Errorf("a refused page was sent")
	}
}

// The account-argument check refuses a foreign AccountId before the identity call and before any request.
func TestRestToolRefusesAForeignAccountBeforeAnyIO(t *testing.T) {
	f := direct(func(sent, int) reply { return regionsAnswer("r1", "") })
	install(t, f)
	s := restRegionsSpec()
	if _, err := s.call(`{"account_id":"210987654321"}`); !isInvalidRequest(err) {
		t.Errorf("a foreign account: %v", err)
	}
	if len(f.requests) != 0 {
		t.Errorf("requests = %v", f.requests)
	}
	if _, err := s.call(`{"account_id":"` + testAccount + `"}`); err != nil {
		t.Errorf("the bound account: %v", err)
	}
	if got := restRequests(f); len(got) != 1 || !strings.Contains(got[0].body, `"AccountId":"`+testAccount+`"`) {
		t.Errorf("requests = %+v", got)
	}
}

// A regional REST tool takes the region from the allowlist, signs for it, and reports it; the token of a
// create is generated, sent in the body and reported; a failure maps through awserr and is not repeated.
func TestRestToolRegionalTokenAndErrors(t *testing.T) {
	status := 200
	f := direct(func(sent, int) reply {
		if status != 200 {
			return reply{status: status, header: map[string]string{"X-Amzn-Errortype": "ConflictException:http://x/"}, body: `{"message":"m"}`}
		}
		return reply{status: 204}
	})
	install(t, f)
	str := &httpbind.Shape{Kind: httpbind.KindString}
	s := toolSpec{
		descriptor: capability.Descriptor{ID: "aws.ses.thing.create", Group: "sts",
			Risk: capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyIdempotent}},
		service: endpoints.SES, op: "create thing",
		rest: &httpbind.Operation{
			Method: "PUT", URI: "/v2/email/things/{Name}",
			Input: &httpbind.Shape{Kind: httpbind.KindStruct, Fields: []httpbind.Field{
				{Key: "name", Name: "Name", Shape: str, Loc: httpbind.LocLabel, Required: true},
				{Key: "client_token", Name: "clientToken", Shape: str, IdempotencyToken: true}}},
		},
		targetCheck: "none", policyCheck: "none",
	}
	if err := s.check(); err != nil {
		t.Fatal(err)
	}
	got, err := s.call(`{"name":"n1","region":"` + testRegion + `"}`)
	calls := restRequests(f)
	if err != nil || got["region"] != testRegion || len(calls) != 1 {
		t.Fatalf("result = %v, %v, calls = %d", got, err, len(calls))
	}
	token, _ := got["client_token"].(string)
	if calls[0].method != "PUT" || calls[0].path != "/v2/email/things/n1" || calls[0].host != "email."+testRegion+".amazonaws.com" ||
		token == "" || !strings.Contains(calls[0].body, `"clientToken":"`+token+`"`) ||
		!strings.Contains(calls[0].header.Get("Authorization"), "/"+testRegion+"/ses/aws4_request") {
		t.Errorf("request = %+v, token %q", calls[0], token)
	}
	status = 409
	before := len(restRequests(f))
	if _, err := s.call(`{"name":"n1"}`); classOf(err) != provider.ClassProviderError || len(restRequests(f)) != before+1 {
		t.Errorf("a conflict: %v after %d requests", err, len(restRequests(f))-before)
	}
	// A missing label is refused before the call.
	before = len(f.requests)
	if _, err := s.call(`{}`); !isInvalidRequest(err) || len(f.requests) != before {
		t.Errorf("a missing label: %v, requests %d", err, len(f.requests)-before)
	}
}

// The registration check refuses a restJson1 spec that mixes the protocols or whose description is faulty.
func TestRestToolSpecIsChecked(t *testing.T) {
	good := restRegionsSpec()
	if err := good.check(); err != nil {
		t.Fatal(err)
	}
	mixed := restRegionsSpec()
	mixed.action = "ListRegions"
	broken := restRegionsSpec()
	broken.rest = &httpbind.Operation{Method: "TRACE", URI: "/x"}
	identity := restRegionsSpec()
	identity.accountCheck = true
	for name, s := range map[string]toolSpec{"mixed": mixed, "broken": broken, "account check": identity} {
		if err := s.check(); err == nil {
			t.Errorf("%s: the spec was accepted", name)
		}
	}
}

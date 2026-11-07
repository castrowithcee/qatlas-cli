package aws

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/awsquery"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/endpoints"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/gen"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

// maxDecodedBytes is the cap of DecodedMessage in the STS curation table (gen/table_sts.go).
const maxDecodedBytes = 32 << 10

// formHolds reports whether any form parameter carries value, such as a generated idempotency token.
func formHolds(form url.Values, value string) bool {
	for _, values := range form {
		if slices.Contains(values, value) {
			return true
		}
	}
	return false
}

func stringShape() *awsquery.Shape { return &awsquery.Shape{Kind: awsquery.KindString} }

// listSpec is a generated tool as the generator writes one for a Marker list, on a made-up action.
func listSpec() toolSpec {
	return toolSpec{
		descriptor: capability.Descriptor{ID: "aws.sts.thing.list", Group: "sts",
			Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe}},
		service: endpoints.STS, action: "ListThings", version: stsVersion, op: "list things",
		input: &awsquery.Shape{Kind: awsquery.KindStruct, Fields: []awsquery.Field{{Key: "prefix", Name: "Prefix", Shape: stringShape()}}},
		output: &awsquery.Shape{Kind: awsquery.KindStruct, Fields: []awsquery.Field{
			{Key: "names", Name: "Names", Shape: &awsquery.Shape{Kind: awsquery.KindList, Elem: stringShape()}},
			{Key: "~IsTruncated", Name: "IsTruncated", Shape: &awsquery.Shape{Kind: awsquery.KindBoolean}},
			{Key: "~Marker", Name: "Marker", Shape: stringShape()}}},
		pager:       &awsquery.Pager{TokenIn: "Marker", TokenOut: "~Marker", Truncated: "~IsTruncated", SizeParam: "MaxItems", MaxSize: 100},
		targetCheck: "none", policyCheck: "none",
	}
}

// call runs a spec's handler on the test connection, past the core, so the fake sees exactly what it sends.
func (s toolSpec) call(args string) (map[string]any, error) {
	red := &redact.Redactor{}
	out, err := s.handler()(context.Background(), connection(), resolverFor(baseValues(), red), red, json.RawMessage(args))
	if err != nil {
		return nil, err
	}
	return out.(map[string]any), nil
}

func listAnswer(names, marker string, truncated bool) reply {
	body := `<ListThingsResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><ListThingsResult><Names>`
	for _, n := range strings.Split(names, ",") {
		body += "<member>" + n + "</member>"
	}
	body += "</Names>"
	if truncated {
		body += "<IsTruncated>true</IsTruncated><Marker>" + marker + "</Marker>"
	} else {
		body += "<IsTruncated>false</IsTruncated>"
	}
	return ok(body + "</ListThingsResult></ListThingsResponse>")
}

// A generated list tool pages over three pages by Marker and IsTruncated, hides the paging members, binds
// the cursor to its arguments, and asks for page_size only within the service's maximum.
func TestGeneratedListPagesWithACursor(t *testing.T) {
	f := direct(func(r sent, _ int) reply {
		switch r.form.Get("Marker") {
		case "":
			return listAnswer("a", "m1", true)
		case "m1":
			return listAnswer("b", "m2", true)
		}
		return listAnswer("c", "", false)
	})
	install(t, f)
	s := listSpec()
	var names []any
	cursor := ""
	for page := 0; page < 3; page++ {
		args := `{"prefix":"p","page_size":2}`
		if cursor != "" {
			args = `{"prefix":"p","page_size":2,"cursor":"` + cursor + `"}`
		}
		got, err := s.call(args)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		names = append(names, got["names"].([]any)...)
		if _, leaked := got["~Marker"]; leaked || got["region"] != testRegion {
			t.Errorf("page %d result = %v", page, got)
		}
		next, _ := got["next_cursor"].(string)
		if got["has_more"] != (page < 2) || (next != "") != (page < 2) || strings.Contains(next, "m1") {
			t.Errorf("page %d: has_more %v, next_cursor %q", page, got["has_more"], next)
		}
		cursor = next
	}
	if !slices.Equal(names, []any{"a", "b", "c"}) || f.count("ListThings") != 3 {
		t.Errorf("names = %v, requests = %v", names, f.actions())
	}
	for _, r := range f.requests {
		if r.action == "ListThings" && (r.form.Get("MaxItems") != "2" || r.form.Get("Prefix") != "p") {
			t.Errorf("form = %v", r.form)
		}
	}

	// A cursor of one list does not continue another, a tampered one is refused, and so is a page size above
	// the maximum: all before any request.
	first, _ := s.call(`{"prefix":"p"}`)
	issued := first["next_cursor"].(string)
	before := len(f.requests)
	for _, args := range []string{`{"prefix":"q","cursor":"` + issued + `"}`, `{"prefix":"p","cursor":"` + issued[:len(issued)-1] + `x"}`,
		`{"prefix":"p","page_size":101}`, `{"prefix":"p","page_size":0}`} {
		if _, err := s.call(args); !isInvalidRequest(err) {
			t.Errorf("%s: %v, want an invalid request", args, err)
		}
	}
	if len(f.requests) != before {
		t.Errorf("a refused page was sent: %v", f.actions()[before:])
	}
}

// A tool with an idempotency token generates it, reports it, and sends a given client_token unchanged. It is
// never repeated after a server failure.
func TestGeneratedTokenIsReportedAndCallerTokenIsUsed(t *testing.T) {
	status := 200
	f := direct(func(sent, int) reply {
		if status != 200 {
			return reply{status: status}
		}
		return ok(`<CreateThingResponse><CreateThingResult><ThingId>t-1</ThingId></CreateThingResult></CreateThingResponse>`)
	})
	install(t, f)
	s := toolSpec{
		descriptor: capability.Descriptor{ID: "aws.sts.thing.create", Group: "sts",
			Risk: capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyIdempotent}},
		service: endpoints.STS, action: "CreateThing", version: stsVersion, op: "create thing",
		input: &awsquery.Shape{Kind: awsquery.KindStruct, Fields: []awsquery.Field{{Key: "name", Name: "Name", Shape: stringShape()},
			{Key: "client_token", Name: "ClientToken", Shape: stringShape(), IdempotencyToken: true}}},
		output:      &awsquery.Shape{Kind: awsquery.KindStruct, Fields: []awsquery.Field{{Key: "thing_id", Name: "ThingId", Shape: stringShape()}}},
		targetCheck: "none", policyCheck: "none",
	}
	got, err := s.call(`{"name":"n"}`)
	if err != nil {
		t.Fatal(err)
	}
	last := f.requests[len(f.requests)-1]
	if token, _ := got["client_token"].(string); token == "" || last.form.Get("ClientToken") != token || got["thing_id"] != "t-1" {
		t.Errorf("result = %v, form = %v", got, last.form)
	}
	got, err = s.call(`{"name":"n","client_token":"repeat-0001"}`)
	if err != nil || got["client_token"] != "repeat-0001" || f.requests[len(f.requests)-1].form.Get("ClientToken") != "repeat-0001" {
		t.Errorf("given token: %v, %v", got, err)
	}
	if _, err := s.call(`{"name":"n","client_token":"has space"}`); !isInvalidRequest(err) {
		t.Errorf("a malformed client_token: %v", err)
	}
	status = 500
	before := f.count("CreateThing")
	if _, err := s.call(`{"name":"n"}`); classOf(err) != provider.ClassProviderError || f.count("CreateThing") != before+1 {
		t.Errorf("a server failure of a create: %v after %d requests", err, f.count("CreateThing")-before)
	}
}

// The target checks judge ARNs and account arguments against the binding before any I/O.
func TestGeneratedTargetChecks(t *testing.T) {
	f := direct(func(sent, int) reply {
		return ok(`<CheckThingResponse><CheckThingResult/></CheckThingResponse>`)
	})
	install(t, f)
	spec := func(check string) toolSpec {
		return toolSpec{
			descriptor: capability.Descriptor{ID: "aws.sts.thing.check", Group: "sts",
				Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe}},
			service: endpoints.STS, action: "CheckThing", version: stsVersion, op: "check thing",
			input:       &awsquery.Shape{Kind: awsquery.KindStruct, Fields: []awsquery.Field{{Key: "target", Name: "Target", Shape: stringShape()}}},
			output:      &awsquery.Shape{Kind: awsquery.KindStruct},
			targetCheck: check, targetArgs: []string{"target"}, policyCheck: "none",
		}
	}
	for _, c := range []struct {
		check, value string
		ok           bool
	}{
		{"account-argument", testAccount, true},
		{"account-argument", "210987654321", false},
		{"arn-account", "arn:aws:iam::" + testAccount + ":role/x", true},
		{"arn-account", "arn:aws:iam::210987654321:role/x", false},
		{"arn-account", "arn:aws-cn:iam::" + testAccount + ":role/x", false},
		{"arn-account", "not-an-arn", false},
		{"arn-region", "arn:aws:sqs:" + testRegion + ":" + testAccount + ":q", true},
		{"arn-region", "arn:aws:sqs:us-west-2:" + testAccount + ":q", false},
		{"arn-region", "arn:aws:sqs:" + testRegion + ":210987654321:q", false},
	} {
		before := len(f.requests)
		_, err := spec(c.check).call(`{"target":"` + c.value + `"}`)
		if c.ok != (err == nil) || (!c.ok && (!isInvalidRequest(err) || len(f.requests) != before)) {
			t.Errorf("%s %s: err = %v, requests %d", c.check, c.value, err, len(f.requests)-before)
		}
		if err != nil && (strings.Contains(err.Error(), c.value) || strings.Contains(err.Error(), testAccount)) {
			t.Errorf("%s: the refusal quotes a value: %v", c.check, err)
		}
	}
}

// A global tool takes no region and calls the global endpoint; the account check uses the first region.
func TestGeneratedGlobalToolHasNoRegion(t *testing.T) {
	f := direct(func(sent, int) reply {
		return ok(`<GetThingResponse><GetThingResult><Name>x</Name></GetThingResult></GetThingResponse>`)
	})
	install(t, f)
	s := toolSpec{
		descriptor: capability.Descriptor{ID: "aws.iam.thing.get", Group: "sts",
			Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe}},
		service: endpoints.IAM, action: "GetThing", version: "2010-05-08", op: "get thing",
		input:       &awsquery.Shape{Kind: awsquery.KindStruct},
		output:      &awsquery.Shape{Kind: awsquery.KindStruct, Fields: []awsquery.Field{{Key: "name", Name: "Name", Shape: stringShape()}}},
		targetCheck: "none", policyCheck: "none",
	}
	got, err := s.call(`{}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := got["region"]; has || got["name"] != "x" {
		t.Errorf("result = %v", got)
	}
	for _, r := range f.requests {
		if r.action == "GetThing" && r.host != "iam.amazonaws.com" {
			t.Errorf("host = %s", r.host)
		}
	}
}

// An answer that misses a required member or fails its form is invalid, never passed on.
func TestGeneratedRulesRefuseInvalidAnswers(t *testing.T) {
	for _, c := range []struct{ tool, args, answer string }{
		{"aws.sts.access-key.get-info", `{"access_key_id":"` + testKeyID + `"}`,
			`<GetAccessKeyInfoResponse><GetAccessKeyInfoResult><Account>1234567890123</Account></GetAccessKeyInfoResult></GetAccessKeyInfoResponse>`},
		{"aws.sts.access-key.get-info", `{"access_key_id":"` + testKeyID + `"}`,
			`<GetAccessKeyInfoResponse><GetAccessKeyInfoResult></GetAccessKeyInfoResult></GetAccessKeyInfoResponse>`},
		{"aws.sts.authorization-message.decode", `{"encoded_message":"x"}`,
			`<DecodeAuthorizationMessageResponse><DecodeAuthorizationMessageResult/></DecodeAuthorizationMessageResponse>`},
	} {
		f := direct(func(sent, int) reply { return ok(c.answer) })
		e := newEnv(t, f, baseValues())
		if _, err := e.invoke(c.tool, c.args); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s with %s: %v, want an invalid response", c.tool, c.answer, err)
		}
	}
}

// A tool that names a check the runtime does not implement is a registration error.
func TestGeneratedSpecsNeedImplementedChecks(t *testing.T) {
	for _, change := range []func(*toolSpec){
		func(s *toolSpec) { s.policyCheck = "trust-policy" },
		func(s *toolSpec) { s.targetCheck = "bucket-owner" },
		func(s *toolSpec) { s.rules = []outputRule{{key: "names", form: "arn-ish"}} },
		func(s *toolSpec) { s.descriptor.Group = "undeclared" },
	} {
		s := listSpec()
		change(&s)
		if s.check() == nil {
			t.Errorf("%+v passed the registration check", s)
		}
	}
	for _, s := range generatedSpecs {
		if err := s.check(); err != nil {
			t.Errorf("%s: %v", s.descriptor.ID, err)
		}
		if s.version != stsVersion && s.service == endpoints.STS {
			t.Errorf("%s: version %s", s.descriptor.ID, s.version)
		}
	}
}

// The runtime implements exactly the names the generator accepts, except the ones that belong elsewhere.
func TestRuntimeChecksMatchTheGeneratorNames(t *testing.T) {
	for name := range targetChecks {
		if !slices.Contains(gen.TargetChecks, name) {
			t.Errorf("target check %s is unknown to the generator", name)
		}
	}
	for _, name := range gen.TargetChecks {
		if _, ok := targetChecks[name]; !ok && name != "bucket-owner" {
			t.Errorf("target check %s has no implementation", name)
		}
	}
	for name := range policyChecks {
		if !slices.Contains(gen.PolicyChecks, name) {
			t.Errorf("policy check %s is unknown to the generator", name)
		}
	}
	for _, name := range gen.Forms {
		if _, ok := forms[name]; !ok {
			t.Errorf("form %s has no implementation", name)
		}
	}
	if len(forms) != len(gen.Forms) {
		t.Errorf("forms = %d, generator knows %d", len(forms), len(gen.Forms))
	}
}

// The decode cap of the table is the one the regression test holds the tool to.
func TestDecodeCapIsTheTableCap(t *testing.T) {
	for _, s := range generatedSpecs {
		if s.descriptor.ID == "aws.sts.authorization-message.decode" &&
			!strings.Contains(string(s.descriptor.OutputSchema), `"decoded_message":{"type":"string","maxLength":32768}`) {
			t.Errorf("output schema = %s", s.descriptor.OutputSchema)
		}
	}
}

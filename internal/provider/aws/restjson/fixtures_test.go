package restjson_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/httpbind"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/restjson"
)

// The operations below are written the way the generator will emit them from the Smithy models of the
// pinned Account (2021-02-01) and SES v2 (2019-09-27) services: the http trait, the member names and their
// places. Cases the two models do not contain (headers, prefix headers, payloads, greedy labels) follow the
// restJson1 protocol tests of Smithy.

type (
	shape = httpbind.Shape
	field = httpbind.Field
)

func str() *shape   { return &shape{Kind: httpbind.KindString} }
func num() *shape   { return &shape{Kind: httpbind.KindInteger} }
func flag() *shape  { return &shape{Kind: httpbind.KindBoolean} }
func blob() *shape  { return &shape{Kind: httpbind.KindBlob} }
func stamp() *shape { return &shape{Kind: httpbind.KindTimestamp} }
func list(e *shape) *shape {
	return &shape{Kind: httpbind.KindList, Elem: e}
}
func mapOf(e *shape) *shape { return &shape{Kind: httpbind.KindMap, Elem: e} }
func obj(fields ...field) *shape {
	return &shape{Kind: httpbind.KindStruct, Fields: fields}
}
func f(key, name string, s *shape) field { return field{Key: key, Name: name, Shape: s} }
func at(loc httpbind.Loc, fl field) field {
	fl.Loc = loc
	return fl
}

// Account: GetContactInformation, POST /getContactInformation, AccountId in the body.
var getContactInformation = &httpbind.Operation{
	Method: "POST", URI: "/getContactInformation",
	Input: obj(f("account_id", "AccountId", str())),
	Output: obj(f("contact", "ContactInformation", obj(
		f("full_name", "FullName", str()), f("phone", "PhoneNumber", str()), f("company", "CompanyName", str())))),
}

// Account: ListRegions pages by NextToken and MaxResults, both in the body of a POST.
var listRegions = &httpbind.Operation{
	Method: "POST", URI: "/listRegions",
	Input: obj(f("account_id", "AccountId", str()), f("~MaxResults", "MaxResults", num()), f("~NextToken", "NextToken", str()),
		f("status_contains", "RegionOptStatusContains", list(str()))),
	Output: obj(f("~NextToken", "NextToken", str()),
		f("regions", "Regions", list(obj(f("name", "RegionName", str()), f("status", "RegionOptStatus", str()))))),
}

var listRegionsPager = httpbind.Pager{TokenIn: "NextToken", TokenOut: "~NextToken", SizeParam: "MaxResults", MaxSize: 50}

// SES v2: GetEmailIdentity, GET /v2/email/identities/{EmailIdentity}.
var getEmailIdentity = &httpbind.Operation{
	Method: "GET", URI: "/v2/email/identities/{EmailIdentity}",
	Input: obj(at(httpbind.LocLabel, f("identity", "EmailIdentity", str()))),
	Output: obj(f("type", "IdentityType", str()), f("sending_enabled", "SendingEnabled", flag()),
		f("verified", "VerifiedForSendingStatus", flag()), f("policies", "Policies", mapOf(str()))),
}

// SES v2: ListEmailIdentities, GET with NextToken and PageSize in the query.
var listEmailIdentities = &httpbind.Operation{
	Method: "GET", URI: "/v2/email/identities",
	Input: obj(at(httpbind.LocQuery, f("~NextToken", "NextToken", str())), at(httpbind.LocQuery, f("~PageSize", "PageSize", num()))),
	Output: obj(f("identities", "EmailIdentities", list(obj(f("name", "IdentityName", str()), f("type", "IdentityType", str())))),
		f("~NextToken", "NextToken", str())),
}

var listEmailIdentitiesPager = httpbind.Pager{TokenIn: "NextToken", TokenOut: "~NextToken", SizeParam: "PageSize", MaxSize: 1000}

// SES v2: ListSuppressedDestinations has a list and timestamps in the query (date-time by default).
var listSuppressed = &httpbind.Operation{
	Method: "GET", URI: "/v2/email/suppression/addresses",
	Input: obj(at(httpbind.LocQuery, f("reasons", "Reason", list(str()))), at(httpbind.LocQuery, f("start", "StartDate", stamp())),
		at(httpbind.LocQuery, f("end", "EndDate", stamp())), at(httpbind.LocQuery, f("~NextToken", "NextToken", str()))),
	Output: obj(f("~NextToken", "NextToken", str())),
}

// SES v2: GetSuppressedDestination, a label and a timestamp in the body (epoch-seconds by default).
var getSuppressed = &httpbind.Operation{
	Method: "GET", URI: "/v2/email/suppression/addresses/{EmailAddress}",
	Input: obj(at(httpbind.LocLabel, f("address", "EmailAddress", str()))),
	Output: obj(f("destination", "SuppressedDestination", obj(
		f("address", "EmailAddress", str()), f("reason", "Reason", str()), f("updated", "LastUpdateTime", stamp())))),
}

// SES v2: CreateConfigurationSet, POST with a body of a string, a list of structures and a structure.
var createConfigurationSet = &httpbind.Operation{
	Method: "POST", URI: "/v2/email/configuration-sets",
	Input: obj(f("name", "ConfigurationSetName", str()),
		f("tags", "Tags", list(obj(f("key", "Key", str()), f("value", "Value", str())))),
		f("sending", "SendingOptions", obj(f("enabled", "SendingEnabled", flag())))),
}

// SES v2: SendEmail with a blob in the body and an idempotency-free mutation.
var sendEmail = &httpbind.Operation{
	Method: "POST", URI: "/v2/email/outbound-emails",
	Input: obj(f("from", "FromEmailAddress", str()),
		f("destination", "Destination", obj(f("to", "ToAddresses", list(str())))),
		f("content", "Content", obj(f("raw", "Raw", obj(f("data", "Data", blob())))))),
	Output: obj(f("message_id", "MessageId", str())),
}

// SES v2: DeleteEmailIdentity, DELETE without a body and an empty answer.
var deleteEmailIdentity = &httpbind.Operation{
	Method: "DELETE", URI: "/v2/email/identities/{EmailIdentity}",
	Input: obj(at(httpbind.LocLabel, f("identity", "EmailIdentity", str()))),
}

// SES v2: PutEmailIdentityDkimAttributes, PUT with a label and a body member.
var putDkim = &httpbind.Operation{
	Method: "PUT", URI: "/v2/email/identities/{EmailIdentity}/dkim",
	Input: obj(at(httpbind.LocLabel, f("identity", "EmailIdentity", str())), f("signing", "SigningEnabled", flag())),
}

// bindings follows the restJson1 protocol tests: a greedy label, headers, a header list, prefix headers,
// query parameters, a jsonName member and a union.
var bindings = &httpbind.Operation{
	Method: "POST", URI: "/bindings/{Bucket}/{Key+}",
	Input: obj(
		at(httpbind.LocLabel, f("bucket", "Bucket", str())),
		at(httpbind.LocLabel, f("object_key", "Key", str())),
		at(httpbind.LocHeader, f("trace", "X-Trace", str())),
		at(httpbind.LocHeader, f("tags", "X-Tags", list(str()))),
		at(httpbind.LocHeader, f("when", "X-When", stamp())),
		at(httpbind.LocHeader, f("count", "X-Count", num())),
		at(httpbind.LocPrefixHeaders, f("meta", "X-Meta-", mapOf(str()))),
		at(httpbind.LocQuery, f("ids", "id", list(num()))),
		at(httpbind.LocQuery, f("flag", "flag", flag())),
		at(httpbind.LocQuery, f("at", "at", stamp())),
		at(httpbind.LocQueryParams, f("params", "ignored", mapOf(str()))),
		f("display", "display-name", str()),
		f("choice", "Choice", &shape{Kind: httpbind.KindStruct, Union: true, Fields: []field{f("a", "A", str()), f("n", "N", num())}}),
	),
	Output: obj(
		at(httpbind.LocHeader, f("trace", "X-Trace", str())),
		at(httpbind.LocHeader, f("tags", "X-Tags", list(str()))),
		at(httpbind.LocHeader, f("when", "X-When", stamp())),
		at(httpbind.LocHeader, f("count", "X-Count", num())),
		at(httpbind.LocPrefixHeaders, f("meta", "X-Meta-", mapOf(str()))),
		at(httpbind.LocResponseCode, f("status", "ignored", num())),
		f("display", "display-name", str()),
		f("choice", "Choice", &shape{Kind: httpbind.KindStruct, Union: true, Fields: []field{f("a", "A", str()), f("n", "N", num())}}),
	),
}

func payloadOp(method string, payload *shape) *httpbind.Operation {
	return &httpbind.Operation{
		Method: method, URI: "/payload",
		Input:  obj(at(httpbind.LocPayload, f("body", "Body", payload)), at(httpbind.LocHeader, f("trace", "X-Trace", str()))),
		Output: obj(at(httpbind.LocPayload, f("body", "Body", payload)), at(httpbind.LocResponseCode, f("status", "ignored", num()))),
	}
}

// wire is one request as the fake endpoint saw it.
type wire struct {
	method, path, rawQuery, body, host string
	header                             http.Header
}

// exchange is a Doer that records the request and answers.
type exchange struct {
	got    []wire
	status int
	header map[string]string
	body   string
	err    error
}

func (e *exchange) Do(req *http.Request) (*http.Response, error) {
	data, _ := io.ReadAll(req.Body)
	e.got = append(e.got, wire{req.Method, req.URL.EscapedPath(), req.URL.RawQuery, string(data), req.URL.Host, req.Header.Clone()})
	if e.err != nil {
		return nil, e.err
	}
	h := http.Header{}
	for k, v := range e.header {
		h.Set(k, v)
	}
	status := e.status
	if status == 0 {
		status = 200
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(e.body))}, nil
}

type signerFunc func(*http.Request, []byte) error

func (s signerFunc) Sign(r *http.Request, b []byte) error { return s(r, b) }

func newClient(e *exchange) *httpbind.Client {
	return restjson.NewClient(httpbind.Client{
		HTTP: e, URL: "https://email.eu-central-1.amazonaws.com", UserAgent: "qatlas/test",
		Signer: signerFunc(func(r *http.Request, _ []byte) error { r.Header.Set("Authorization", "signed"); return nil }),
	})
}

// call encodes and sends one operation against e.
func call(t *testing.T, e *exchange, o *httpbind.Operation, args map[string]any) (map[string]any, httpbind.Meta, *provider.Error) {
	t.Helper()
	req, err := o.Encode(restjson.Codec{}, args, nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var out map[string]any
	meta, failure := newClient(e).Do(context.Background(), "aws.test", o, req, &out)
	return out, meta, failure
}

func encode(t *testing.T, o *httpbind.Operation, args map[string]any) httpbind.Request {
	t.Helper()
	req, err := o.Encode(restjson.Codec{}, args, nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return req
}

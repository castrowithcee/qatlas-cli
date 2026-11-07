package restjson_test

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/httpbind"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/sigv4"
)

func TestRequestOnTheWire(t *testing.T) {
	e := &exchange{body: `{"IdentityType":"DOMAIN","SendingEnabled":true}`, header: map[string]string{"X-Amzn-Requestid": "req-1"}}
	out, meta, failure := call(t, e, getEmailIdentity, map[string]any{"identity": "a b"})
	if failure != nil {
		t.Fatal(failure)
	}
	w := e.got[0]
	if w.method != "GET" || w.host != "email.eu-central-1.amazonaws.com" || w.path != "/v2/email/identities/a%20b" {
		t.Errorf("wire = %+v", w)
	}
	if w.header.Get("Accept") != "application/json" || w.header.Get("Content-Type") != "" || w.header.Get("Authorization") != "signed" ||
		w.header.Get("User-Agent") != "qatlas/test" || w.body != "" {
		t.Errorf("headers = %v, body %q", w.header, w.body)
	}
	if meta.RequestID != "req-1" || out["type"] != "DOMAIN" || out["sending_enabled"] != true {
		t.Errorf("out = %v, meta = %+v", out, meta)
	}

	e = &exchange{body: `{}`}
	if _, _, failure = call(t, e, createConfigurationSet, map[string]any{"name": "n"}); failure != nil {
		t.Fatal(failure)
	}
	if w = e.got[0]; w.method != "POST" || w.header.Get("Content-Type") != "application/json" || w.body != `{"ConfigurationSetName":"n"}` {
		t.Errorf("POST wire = %+v", w)
	}
}

func TestEndpointMustBeHTTPSWithoutPath(t *testing.T) {
	for _, u := range []string{"http://email.eu-central-1.amazonaws.com", "https://host/prefix", "email", ""} {
		c := newClient(&exchange{})
		c.URL = u
		req := encode(t, getEmailIdentity, map[string]any{"identity": "x"})
		if _, failure := c.Do(context.Background(), "aws.test", getEmailIdentity, req, nil); failure == nil {
			t.Errorf("endpoint %q was accepted", u)
		}
	}
}

func TestRealSignatureCoversTheEscapedPathAndBody(t *testing.T) {
	var auth string
	e := &exchange{body: `{}`}
	signer := &sigv4.Signer{Credentials: sigv4.Credentials{AccessKeyID: "AKEXAMPLEKEY", SecretAccessKey: "secret-for-test"}, Region: "eu-central-1", Service: "ses",
		Clock: sigv4.ClockFunc(func() time.Time { return time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC) })}
	c := newClient(e)
	c.Signer = signer
	op := putDkim
	send := func(identity string) string {
		req := encode(t, op, map[string]any{"identity": identity, "signing": true})
		if _, failure := c.Do(context.Background(), "aws.test", op, req, nil); failure != nil {
			t.Fatal(failure)
		}
		return e.got[len(e.got)-1].header.Get("Authorization")
	}
	auth = send("a/b")
	if !strings.Contains(auth, "SignedHeaders=accept;content-type;host;x-amz-date") || strings.Contains(auth, "x-amz-content-sha256") {
		t.Errorf("Authorization = %s", auth)
	}
	if e.got[0].path != "/v2/email/identities/a%2Fb/dkim" {
		t.Errorf("path = %s", e.got[0].path)
	}
	// Another identity must give another signature: the path is part of what is signed.
	if send("a-b") == auth {
		t.Error("the signature does not depend on the path")
	}
}

func TestSuppressedDestinationDecodesEpochTimestamps(t *testing.T) {
	for _, stamp := range []string{"1.616434368E9", "1616434368", "1616434368.000"} {
		e := &exchange{body: `{"SuppressedDestination":{"EmailAddress":"x","Reason":"BOUNCE","LastUpdateTime":` + stamp + `,"Extra":1}}`}
		out, _, failure := call(t, e, getSuppressed, map[string]any{"address": "x"})
		if failure != nil {
			t.Fatalf("%s: %v", stamp, failure)
		}
		want := map[string]any{"destination": map[string]any{"address": "x", "reason": "BOUNCE", "updated": "2021-03-22T17:32:48Z"}}
		if !reflect.DeepEqual(out, want) {
			t.Errorf("%s: out = %v", stamp, out)
		}
	}
}

func TestResponseHeadersPrefixHeadersStatusAndBody(t *testing.T) {
	e := &exchange{
		status: 201,
		header: map[string]string{
			"X-Trace": "abc", "X-Tags": `a, "b,c", "q\"r"`, "X-When": "Mon, 06 May 2024 07:08:09 GMT", "X-Count": "3",
			"X-Meta-One": "1", "x-meta-two": "2", "X-Other": "ignored",
		},
		body: `{"display-name":"n","Choice":{"N":4},"Unknown":{"x":1}}`,
	}
	out, _, failure := call(t, e, bindings, map[string]any{"bucket": "b", "object_key": "k"})
	if failure != nil {
		t.Fatal(failure)
	}
	want := map[string]any{
		"trace": "abc", "tags": []any{"a", "b,c", `q"r`}, "when": "2024-05-06T07:08:09Z", "count": int64(3),
		"meta": map[string]any{"one": "1", "two": "2"}, "status": int64(201),
		"display": "n", "choice": map[string]any{"n": int64(4)},
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("out = %#v", out)
	}
}

func TestEmptyAnswers(t *testing.T) {
	// 204 without a body and without members.
	e := &exchange{status: 204}
	out, _, failure := call(t, e, deleteEmailIdentity, map[string]any{"identity": "x"})
	if failure != nil || len(out) != 0 {
		t.Errorf("204: out = %v, failure = %v", out, failure)
	}
	// 204 with the status member and a header.
	e = &exchange{status: 204, header: map[string]string{"X-Count": "7"}}
	out, _, failure = call(t, e, bindings, map[string]any{"bucket": "b", "object_key": "k"})
	if failure != nil || !reflect.DeepEqual(out, map[string]any{"status": int64(204), "count": int64(7)}) {
		t.Errorf("204 with members: out = %v, failure = %v", out, failure)
	}
	// An empty body where the output has document members, and an empty object.
	for _, body := range []string{"", "  ", `{}`} {
		out, _, failure = call(t, &exchange{body: body}, getEmailIdentity, map[string]any{"identity": "x"})
		if failure != nil || len(out) != 0 {
			t.Errorf("body %q: out = %v, failure = %v", body, out, failure)
		}
	}
	// A body where the output has no member for it is not read.
	if _, _, failure = call(t, &exchange{body: "<html>not json</html>"}, deleteEmailIdentity, map[string]any{"identity": "x"}); failure != nil {
		t.Errorf("an unread body failed the call: %v", failure)
	}
}

func TestPayloadAnswers(t *testing.T) {
	structure := obj(f("name", "Name", str()))
	cases := []struct {
		name    string
		payload *shape
		body    string
		want    any
		bad     bool
	}{
		{"blob", blob(), "hello", "aGVsbG8=", false},
		{"string", str(), "plain", "plain", false},
		{"structure", structure, `{"Name":"x"}`, map[string]any{"name": "x"}, false},
		{"document", &shape{Kind: httpbind.KindDocument}, `{"a":[1,null,"s"]}`, map[string]any{"a": []any{json.Number("1"), nil, "s"}}, false},
		{"structure not json", structure, `Name=x`, nil, true},
		{"structure of a list", structure, `["x"]`, nil, true},
	}
	for _, c := range cases {
		out, _, failure := call(t, &exchange{body: c.body}, payloadOp("PUT", c.payload), map[string]any{})
		switch {
		case c.bad && (failure == nil || failure.Class != provider.ClassInvalidResponse):
			t.Errorf("%s: failure = %v", c.name, failure)
		case !c.bad && (failure != nil || !reflect.DeepEqual(out["body"], c.want)):
			t.Errorf("%s: out = %#v, failure = %v", c.name, out, failure)
		}
	}
	// An empty payload is absent, not an empty value.
	out, _, failure := call(t, &exchange{status: 200}, payloadOp("PUT", blob()), map[string]any{})
	if failure != nil || !reflect.DeepEqual(out, map[string]any{"status": int64(200)}) {
		t.Errorf("empty payload: %v %v", out, failure)
	}
}

func TestUnreadableOrMisshapenAnswersAreInvalid(t *testing.T) {
	for name, body := range map[string]string{
		"not json":           `<html>`,
		"truncated":          `{"IdentityType":"DOM`,
		"trailing value":     `{} {}`,
		"trailing garbage":   `{}x`,
		"array at the top":   `[]`,
		"repeated key":       `{"IdentityType":"a","IdentityType":"b"}`,
		"string for boolean": `{"SendingEnabled":"true"}`,
		"number for string":  `{"IdentityType":1}`,
		"map value wrong":    `{"Policies":{"a":1}}`,
		"map value null":     `{"Policies":{"a":null}}`,
		"list for map":       `{"Policies":[]}`,
		"too deep":           strings.Repeat(`{"a":`, 40) + `1` + strings.Repeat(`}`, 40),
		"invalid escape":     `{"IdentityType":"\x"}`,
	} {
		_, _, failure := call(t, &exchange{body: body}, getEmailIdentity, map[string]any{"identity": "x"})
		if failure == nil || failure.Class != provider.ClassInvalidResponse {
			t.Errorf("%s: failure = %v", name, failure)
		}
	}
	// A list over its cap is refused, as cutting it would present a partial list as complete.
	op := &httpbind.Operation{Method: "POST", URI: "/l", Output: obj(f("l", "L", &shape{Kind: httpbind.KindList, Elem: str(), MaxItems: 2}))}
	if _, _, failure := call(t, &exchange{body: `{"L":["a","b","c"]}`}, op, nil); failure == nil || failure.Class != provider.ClassInvalidResponse {
		t.Errorf("list over the cap: %v", failure)
	}
	if out, _, failure := call(t, &exchange{body: `{"L":["a","b"]}`}, op, nil); failure != nil || len(out["l"].([]any)) != 2 {
		t.Errorf("list at the cap: %v %v", out, failure)
	}
	// An unreadable body of a failed answer does not hide the failure either (see the error tests).
}

func TestStringsAreCappedAndReported(t *testing.T) {
	op := &httpbind.Operation{Method: "POST", URI: "/s", Output: obj(f("s", "S", &shape{Kind: httpbind.KindString, MaxLen: 4}))}
	out, meta, failure := call(t, &exchange{body: `{"S":"aé€x"}`}, op, nil)
	// 4 bytes cut inside the euro sign must end on a rune boundary.
	if failure != nil || out["s"] != "aé" || !meta.Truncated {
		t.Errorf("out = %v, meta = %+v, failure = %v", out, meta, failure)
	}
	if _, meta, _ = call(t, &exchange{body: `{"S":"abcd"}`}, op, nil); meta.Truncated {
		t.Error("a string at the cap was reported as cut")
	}
}

func TestResponseLimit(t *testing.T) {
	e := &exchange{body: `{"IdentityType":"` + strings.Repeat("x", 100) + `"}`}
	c := newClient(e)
	c.ResponseLimit = 50
	req := encode(t, getEmailIdentity, map[string]any{"identity": "x"})
	if _, failure := c.Do(context.Background(), "aws.test", getEmailIdentity, req, nil); failure == nil || failure.Class != provider.ClassInvalidResponse {
		t.Errorf("failure = %v", failure)
	}
}

func TestDocumentAnswerIsBoundedAndExact(t *testing.T) {
	op := payloadOp("PUT", &shape{Kind: httpbind.KindDocument, MaxItems: 3})
	out, _, failure := call(t, &exchange{body: `{"a":12345678901234567890,"b":"x","c":true}`}, op, nil)
	if failure != nil || out["body"].(map[string]any)["a"] != json.Number("12345678901234567890") {
		t.Errorf("out = %v, failure = %v", out, failure)
	}
	if _, _, failure = call(t, &exchange{body: `[1,2,3,4]`}, op, nil); failure == nil {
		t.Error("a document list over the cap was accepted")
	}
}

func TestTransportFailureIsNotAnAnswer(t *testing.T) {
	_, _, failure := call(t, &exchange{err: context.DeadlineExceeded}, getEmailIdentity, map[string]any{"identity": "x"})
	if failure == nil || failure.Class != provider.ClassTimeout {
		t.Errorf("failure = %v", failure)
	}
}

func TestSignerFailureSendsNothing(t *testing.T) {
	e := &exchange{}
	c := newClient(e)
	c.Signer = signerFunc(func(*http.Request, []byte) error { return context.Canceled })
	req := encode(t, getEmailIdentity, map[string]any{"identity": "x"})
	if _, failure := c.Do(context.Background(), "aws.test", getEmailIdentity, req, nil); failure == nil || len(e.got) != 0 {
		t.Errorf("failure = %v, requests = %d", failure, len(e.got))
	}
}

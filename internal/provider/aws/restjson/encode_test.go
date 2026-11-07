package restjson_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/httpbind"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/restjson"
)

func TestModelOperationsValidate(t *testing.T) {
	for name, o := range map[string]*httpbind.Operation{
		"getContactInformation": getContactInformation, "listRegions": listRegions, "getEmailIdentity": getEmailIdentity,
		"listEmailIdentities": listEmailIdentities, "listSuppressed": listSuppressed, "getSuppressed": getSuppressed,
		"createConfigurationSet": createConfigurationSet, "sendEmail": sendEmail, "deleteEmailIdentity": deleteEmailIdentity,
		"putDkim": putDkim, "bindings": bindings, "payload": payloadOp("PUT", str()),
	} {
		if err := o.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLabelInThePath(t *testing.T) {
	req := encode(t, getEmailIdentity, map[string]any{"identity": "example.org"})
	if req.Method != "GET" || req.RawPath != "/v2/email/identities/example.org" || req.RawQuery != "" || len(req.Body) != 0 {
		t.Fatalf("request = %+v", req)
	}
	if req.Header.Get("Content-Type") != "" {
		t.Errorf("a GET without a body sent Content-Type %q", req.Header.Get("Content-Type"))
	}
	// A label is URL-encoded; the decoded path is the one that is signed once more.
	req = encode(t, getSuppressed, map[string]any{"address": "a+b/c d@example.com"})
	if req.RawPath != "/v2/email/suppression/addresses/a%2Bb%2Fc%20d%40example.com" || req.Path != "/v2/email/suppression/addresses/a+b/c d@example.com" {
		t.Errorf("path = %q / %q", req.RawPath, req.Path)
	}
}

func TestGreedyLabelKeepsSlashes(t *testing.T) {
	req := encode(t, bindings, map[string]any{"bucket": "b k", "object_key": "dir/sub dir/é.txt"})
	if req.RawPath != "/bindings/b%20k/dir/sub%20dir/%C3%A9.txt" {
		t.Errorf("path = %q", req.RawPath)
	}
}

func TestLabelsThatWouldChangeThePathAreRefused(t *testing.T) {
	cases := map[string]map[string]any{
		"empty bucket":            {"bucket": "", "object_key": "k"},
		"dot bucket":              {"bucket": "..", "object_key": "k"},
		"single dot":              {"bucket": ".", "object_key": "k"},
		"dot segment in greedy":   {"bucket": "b", "object_key": "a/../b"},
		"empty segment":           {"bucket": "b", "object_key": "a//b"},
		"trailing slash":          {"bucket": "b", "object_key": "a/"},
		"leading slash":           {"bucket": "b", "object_key": "/a"},
		"control character":       {"bucket": "b\n", "object_key": "k"},
		"label missing":           {"bucket": "b"},
		"label of the wrong type": {"bucket": 5, "object_key": "k"},
	}
	for name, args := range cases {
		if _, err := bindings.Encode(restjson.Codec{}, args, nil); !errors.Is(err, httpbind.ErrValue) {
			t.Errorf("%s: err = %v, want ErrValue", name, err)
		}
	}
	// A slash in a label that is not greedy is encoded, not refused: it cannot add a segment.
	req := encode(t, getEmailIdentity, map[string]any{"identity": "a/b"})
	if req.RawPath != "/v2/email/identities/a%2Fb" {
		t.Errorf("path = %q", req.RawPath)
	}
	if _, err := getEmailIdentity.Encode(restjson.Codec{}, map[string]any{"identity": "../x"}, nil); err != nil {
		// "../x" has a slash that is encoded, so the segment is "..%2Fx": allowed and harmless.
		t.Errorf("an encoded slash must stay in its segment: %v", err)
	}
}

func TestQueryListsAndTimestamps(t *testing.T) {
	req := encode(t, listSuppressed, map[string]any{
		"reasons":    []any{"BOUNCE", "COMPLAINT"},
		"start":      "2024-01-02T03:04:05Z",
		"end":        "2024-01-03T03:04:05.5+01:00",
		"~NextToken": "a b&c=d",
	})
	// Members are sorted by name, a list keeps its order.
	want := "EndDate=2024-01-03T02%3A04%3A05.5Z&NextToken=a%20b%26c%3Dd&Reason=BOUNCE&Reason=COMPLAINT&StartDate=2024-01-02T03%3A04%3A05Z"
	if req.RawQuery != want {
		t.Errorf("query = %s\nwant    %s", req.RawQuery, want)
	}
	req = encode(t, listSuppressed, map[string]any{"reasons": []any{}})
	if req.RawQuery != "" {
		t.Errorf("an empty list must send nothing, query = %q", req.RawQuery)
	}
}

func TestQueryPagingForGET(t *testing.T) {
	req := encode(t, listEmailIdentities, map[string]any{"~NextToken": "tok", "~PageSize": json.Number("20")})
	if req.RawQuery != "NextToken=tok&PageSize=20" || len(req.Body) != 0 {
		t.Errorf("request = %+v", req)
	}
}

func TestHeadersPrefixHeadersAndQueryParams(t *testing.T) {
	req := encode(t, bindings, map[string]any{
		"bucket": "b", "object_key": "k",
		"trace":  "abc",
		"tags":   []any{"a", "b,c", `q"r`},
		"when":   "2024-05-06T07:08:09Z",
		"count":  json.Number("3"),
		"meta":   map[string]any{"one": "1", "Two": "2"},
		"ids":    []any{json.Number("3"), json.Number("1")},
		"flag":   true,
		"at":     "2024-05-06T07:08:09Z",
		"params": map[string]any{"zeta": "z", "alpha": "a"},
	})
	h := req.Header
	if h.Get("X-Trace") != "abc" || h.Get("X-Count") != "3" || h.Get("X-When") != "Mon, 06 May 2024 07:08:09 GMT" {
		t.Errorf("headers = %v", h)
	}
	if got := h.Get("X-Tags"); got != `a,"b,c","q\"r"` {
		t.Errorf("list header = %s", got)
	}
	if h.Get("X-Meta-one") != "1" || h.Get("X-Meta-Two") != "2" {
		t.Errorf("prefix headers = %v", h)
	}
	if req.RawQuery != "alpha=a&at=2024-05-06T07%3A08%3A09Z&flag=true&id=3&id=1&zeta=z" {
		t.Errorf("query = %s", req.RawQuery)
	}
	// With no body member set, an input that has document members still sends an empty document.
	if string(req.Body) != `{}` || h.Get("Content-Type") != "application/json" {
		t.Errorf("body = %q, content type = %q", req.Body, h.Get("Content-Type"))
	}
}

func TestHeaderAndQueryRefusals(t *testing.T) {
	base := func(k string, v any) map[string]any {
		m := map[string]any{"bucket": "b", "object_key": "k"}
		m[k] = v
		return m
	}
	for name, args := range map[string]map[string]any{
		"header injection":            base("trace", "x\r\nHost: evil"),
		"non ascii header":            base("trace", "é"),
		"prefix header injection":     base("meta", map[string]any{"a": "x\ny"}),
		"prefix key with a space":     base("meta", map[string]any{"a b": "x"}),
		"prefix header without a key": base("meta", map[string]any{"": "x"}),
		"query param name collision":  base("params", map[string]any{"flag": "x"}),
		"query list element is null":  base("ids", []any{nil}),
		"timestamp not RFC 3339":      base("at", "yesterday"),
		"integer header as text":      base("count", "3"),
		"unknown argument":            base("nope", "x"),
	} {
		if name == "query param name collision" {
			args["flag"] = true
		}
		if _, err := bindings.Encode(restjson.Codec{}, args, nil); !errors.Is(err, httpbind.ErrValue) {
			t.Errorf("%s: err = %v, want ErrValue", name, err)
		}
	}
	// The error names the member, never the value.
	_, err := bindings.Encode(restjson.Codec{}, base("trace", "secret-value\n"), nil)
	if err == nil || strings.Contains(err.Error(), "secret-value") {
		t.Errorf("error leaks the value: %v", err)
	}
}

func TestTimestampFormatsInEveryPlace(t *testing.T) {
	const instant = "2024-05-06T07:08:09.25Z"
	at := func(format httpbind.Format) *shape { return &shape{Kind: httpbind.KindTimestamp, Format: format} }
	op := func(loc httpbind.Loc, name string, s *shape) *httpbind.Operation {
		method := "GET"
		in := obj(at2(loc, f("t", name, s)))
		if loc == httpbind.LocBody {
			method = "POST"
		}
		return &httpbind.Operation{Method: method, URI: "/t", Input: in}
	}
	type result struct{ query, header, body string }
	cases := []struct {
		name   string
		format httpbind.Format
		want   result
	}{
		{"default", httpbind.FormatDefault, result{"t=2024-05-06T07%3A08%3A09.25Z", "refused", `{"t":1714979289.25}`}},
		{"epoch-seconds", httpbind.FormatEpochSeconds, result{"t=1714979289.25", "1714979289.25", `{"t":1714979289.25}`}},
		{"date-time", httpbind.FormatDateTime, result{"t=2024-05-06T07%3A08%3A09.25Z", "2024-05-06T07:08:09.25Z", `{"t":"2024-05-06T07:08:09.25Z"}`}},
		{"http-date", httpbind.FormatHTTPDate, result{"refused", "refused", "refused"}},
	}
	for _, c := range cases {
		check := func(place string, o *httpbind.Operation, want string) {
			req, err := o.Encode(restjson.Codec{}, map[string]any{"t": instant}, nil)
			var got string
			switch {
			case err != nil:
				got = "refused"
			case place == "query":
				got = req.RawQuery
			case place == "header":
				got = req.Header.Get("X-T")
			default:
				got = string(req.Body)
			}
			if got != want {
				t.Errorf("%s in %s: got %q, want %q", c.name, place, got, want)
			}
		}
		check("query", op(httpbind.LocQuery, "t", at(c.format)), c.want.query)
		check("header", op(httpbind.LocHeader, "X-T", at(c.format)), c.want.header)
		check("body", op(httpbind.LocBody, "t", at(c.format)), c.want.body)
	}
	// The second-precision instant has an http-date form, and the default of a header is http-date.
	req, err := (&httpbind.Operation{Method: "GET", URI: "/t", Input: obj(at2(httpbind.LocHeader, f("t", "X-T", stamp())))}).
		Encode(restjson.Codec{}, map[string]any{"t": "2024-05-06T07:08:09Z"}, nil)
	if err != nil || req.Header.Get("X-T") != "Mon, 06 May 2024 07:08:09 GMT" {
		t.Errorf("header default = %v %v", req.Header, err)
	}
	// epoch-seconds in the body for a whole second, and no form before 1970.
	body := encode(t, &httpbind.Operation{Method: "POST", URI: "/t", Input: obj(f("t", "t", stamp()))}, map[string]any{"t": "2024-05-06T07:08:09Z"})
	if string(body.Body) != `{"t":1714979289}` {
		t.Errorf("body = %s", body.Body)
	}
	if _, err := (&httpbind.Operation{Method: "POST", URI: "/t", Input: obj(f("t", "t", stamp()))}).
		Encode(restjson.Codec{}, map[string]any{"t": "1960-01-01T00:00:00Z"}, nil); err == nil {
		t.Error("a time before 1970 has no epoch-seconds form")
	}
}

func at2(loc httpbind.Loc, fl field) field { fl.Loc = loc; return fl }

func TestJSONBody(t *testing.T) {
	req := encode(t, createConfigurationSet, map[string]any{
		"name": "set-1",
		"tags": []any{map[string]any{"key": "k", "value": "<v>&"}},
		// A false boolean and a zero are values, not absent members.
		"sending": map[string]any{"enabled": false},
	})
	want := `{"ConfigurationSetName":"set-1","Tags":[{"Key":"k","Value":"<v>&"}],"SendingOptions":{"SendingEnabled":false}}`
	if string(req.Body) != want || req.Method != "POST" || req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("body = %s\ncontent type = %q", req.Body, req.Header.Get("Content-Type"))
	}
	// A null member is absent.
	req = encode(t, createConfigurationSet, map[string]any{"name": "n", "tags": nil})
	if string(req.Body) != `{"ConfigurationSetName":"n"}` {
		t.Errorf("body = %s", req.Body)
	}
}

func TestAccountIDGoesIntoTheBody(t *testing.T) {
	req := encode(t, getContactInformation, map[string]any{"account_id": "123456789012"})
	if string(req.Body) != `{"AccountId":"123456789012"}` || req.RawPath != "/getContactInformation" {
		t.Errorf("request = %s %s", req.RawPath, req.Body)
	}
	req = encode(t, listRegions, map[string]any{"~MaxResults": json.Number("10"), "status_contains": []any{"ENABLED", "DISABLED"}})
	if string(req.Body) != `{"MaxResults":10,"RegionOptStatusContains":["ENABLED","DISABLED"]}` {
		t.Errorf("body = %s", req.Body)
	}
}

func TestBlobIsBase64InTheBodyAndRawInAPayload(t *testing.T) {
	req := encode(t, sendEmail, map[string]any{"from": "sender", "content": map[string]any{"raw": map[string]any{"data": "aGVsbG8="}}})
	if string(req.Body) != `{"FromEmailAddress":"sender","Content":{"Raw":{"Data":"aGVsbG8="}}}` {
		t.Errorf("body = %s", req.Body)
	}
	if _, err := sendEmail.Encode(restjson.Codec{}, map[string]any{"content": map[string]any{"raw": map[string]any{"data": "not base64!"}}}, nil); err == nil {
		t.Error("a blob that is not base64 was accepted")
	}
	req = encode(t, payloadOp("PUT", blob()), map[string]any{"body": "aGVsbG8="})
	if string(req.Body) != "hello" || req.Header.Get("Content-Type") != "application/octet-stream" {
		t.Errorf("blob payload = %q, %q", req.Body, req.Header.Get("Content-Type"))
	}
}

func TestPayloadMembers(t *testing.T) {
	structure := obj(f("name", "Name", str()), f("n", "N", num()))
	req := encode(t, payloadOp("POST", structure), map[string]any{"body": map[string]any{"name": "x", "n": json.Number("2")}, "trace": "t"})
	if string(req.Body) != `{"Name":"x","N":2}` || req.Header.Get("Content-Type") != "application/json" || req.Header.Get("X-Trace") != "t" {
		t.Errorf("structure payload = %s %v", req.Body, req.Header)
	}
	// An empty structure is an empty document; an unset payload is no body at all.
	if req = encode(t, payloadOp("POST", structure), map[string]any{"body": map[string]any{}}); string(req.Body) != `{}` {
		t.Errorf("empty structure payload = %q", req.Body)
	}
	if req = encode(t, payloadOp("POST", structure), map[string]any{}); len(req.Body) != 0 || req.Header.Get("Content-Type") != "" {
		t.Errorf("unset payload = %q %v", req.Body, req.Header)
	}
	req = encode(t, payloadOp("PUT", str()), map[string]any{"body": "plain text"})
	if string(req.Body) != "plain text" || req.Header.Get("Content-Type") != "text/plain" {
		t.Errorf("string payload = %q, %q", req.Body, req.Header.Get("Content-Type"))
	}
	doc := encode(t, payloadOp("PUT", &shape{Kind: httpbind.KindDocument}), map[string]any{"body": map[string]any{"b": []any{true, nil, json.Number("1.5")}, "a": "x"}})
	if string(doc.Body) != `{"a":"x","b":[true,null,1.5]}` {
		t.Errorf("document payload = %s", doc.Body)
	}
}

func TestBodyRulesPerMethod(t *testing.T) {
	// GET and DELETE carry no body, whatever the arguments.
	for _, o := range []*httpbind.Operation{getEmailIdentity, deleteEmailIdentity, listEmailIdentities} {
		args := map[string]any{}
		if len(o.Input.Fields) > 0 && o.Input.Fields[0].Loc == httpbind.LocLabel {
			args["identity"] = "x"
		}
		if req := encode(t, o, args); len(req.Body) != 0 || req.Header.Get("Content-Type") != "" {
			t.Errorf("%s sent a body %q", o.Method, req.Body)
		}
	}
	// A description that gives a GET or DELETE a body member is refused instead of guessed.
	for _, method := range []string{"GET", "DELETE", "HEAD"} {
		o := &httpbind.Operation{Method: method, URI: "/x", Input: obj(f("a", "A", str()))}
		if err := o.Validate(); !errors.Is(err, httpbind.ErrShape) {
			t.Errorf("%s with a body member: %v", method, err)
		}
	}
	// PUT with a label and a body member sends the member.
	req := encode(t, putDkim, map[string]any{"identity": "example.org", "signing": true})
	if req.Method != "PUT" || req.RawPath != "/v2/email/identities/example.org/dkim" || string(req.Body) != `{"SigningEnabled":true}` {
		t.Errorf("request = %+v %s", req, req.Body)
	}
}

func TestJSONNameAndUnion(t *testing.T) {
	req := encode(t, bindings, map[string]any{"bucket": "b", "object_key": "k", "display": "n", "choice": map[string]any{"n": json.Number("4")}})
	if string(req.Body) != `{"display-name":"n","Choice":{"N":4}}` {
		t.Errorf("body = %s", req.Body)
	}
	for name, choice := range map[string]map[string]any{
		"two members":  {"a": "x", "n": json.Number("1")},
		"no member":    {},
		"unknown":      {"z": "x"},
		"wrong member": {"n": "four"},
	} {
		if _, err := bindings.Encode(restjson.Codec{}, map[string]any{"bucket": "b", "object_key": "k", "choice": choice}, nil); !errors.Is(err, httpbind.ErrValue) {
			t.Errorf("union with %s: %v", name, err)
		}
	}
}

func TestNumbersAndBooleansAreExact(t *testing.T) {
	o := &httpbind.Operation{Method: "POST", URI: "/n", Input: obj(
		f("i", "I", num()), f("fl", "F", &shape{Kind: httpbind.KindFloat}), f("b", "B", flag()), f("big", "Big", num()))}
	req := encode(t, o, map[string]any{"i": json.Number("-7"), "fl": 1.5, "b": false, "big": json.Number("9007199254740993")})
	if string(req.Body) != `{"I":-7,"F":1.5,"B":false,"Big":9007199254740993}` {
		t.Errorf("body = %s", req.Body)
	}
	for name, args := range map[string]map[string]any{
		"fraction for an integer": {"i": 1.5},
		"string for an integer":   {"i": "1"},
		"NaN":                     {"fl": nan()},
		"string for a boolean":    {"b": "true"},
	} {
		if _, err := o.Encode(restjson.Codec{}, args, nil); !errors.Is(err, httpbind.ErrValue) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDocumentIsBounded(t *testing.T) {
	o := payloadOp("PUT", &shape{Kind: httpbind.KindDocument})
	deep := any("x")
	for range 40 {
		deep = []any{deep}
	}
	if _, err := o.Encode(restjson.Codec{}, map[string]any{"body": deep}, nil); !errors.Is(err, httpbind.ErrValue) {
		t.Errorf("deep document: %v", err)
	}
	if _, err := o.Encode(restjson.Codec{}, map[string]any{"body": struct{ A int }{1}}, nil); !errors.Is(err, httpbind.ErrValue) {
		t.Errorf("a Go struct is not a JSON value: %v", err)
	}
}

func TestIdempotencyToken(t *testing.T) {
	o := &httpbind.Operation{Method: "POST", URI: "/t", Input: obj(
		field{Key: "client_token", Name: "ClientToken", Shape: str(), IdempotencyToken: true}, f("name", "Name", str()))}
	req := encode(t, o, map[string]any{"name": "n"})
	var sent map[string]string
	if err := json.Unmarshal(req.Body, &sent); err != nil || req.Token == "" || !req.Generated || sent["ClientToken"] != req.Token {
		t.Errorf("generated token: %+v %s", req, req.Body)
	}
	req = encode(t, o, map[string]any{"client_token": "my-token-1"})
	if req.Token != "my-token-1" || req.Generated || string(req.Body) != `{"ClientToken":"my-token-1"}` {
		t.Errorf("given token: %+v %s", req, req.Body)
	}
	if _, err := o.Encode(restjson.Codec{}, map[string]any{"client_token": "has space"}, nil); err == nil {
		t.Error("a malformed token was accepted")
	}
	// The token can sit in a header as well.
	h := &httpbind.Operation{Method: "POST", URI: "/t", Input: obj(
		field{Key: "client_token", Name: "X-Client-Token", Shape: str(), IdempotencyToken: true, Loc: httpbind.LocHeader})}
	if req = encode(t, h, nil); req.Header.Get("X-Client-Token") != req.Token || req.Token == "" {
		t.Errorf("header token: %+v", req)
	}
}

func TestValidateRefusesWhatIsNotBoundClearly(t *testing.T) {
	label := at2(httpbind.LocLabel, f("l", "L", str()))
	cases := map[string]*httpbind.Operation{
		"unknown method":               {Method: "TRACE", URI: "/x"},
		"uri without slash":            {Method: "GET", URI: "x"},
		"dot segment":                  {Method: "GET", URI: "/a/../b"},
		"greedy label in the middle":   {Method: "GET", URI: "/{A+}/b", Input: obj(at2(httpbind.LocLabel, f("a", "A", str())))},
		"label without member":         {Method: "GET", URI: "/{L}"},
		"label member without pattern": {Method: "GET", URI: "/x", Input: obj(label)},
		"label of a list":              {Method: "GET", URI: "/{L}", Input: obj(at2(httpbind.LocLabel, f("l", "L", list(str()))))},
		"reserved header":              {Method: "GET", URI: "/x", Input: obj(at2(httpbind.LocHeader, f("h", "Authorization", str())))},
		"content type header":          {Method: "GET", URI: "/x", Input: obj(at2(httpbind.LocHeader, f("h", "Content-Type", str())))},
		"two payloads":                 {Method: "PUT", URI: "/x", Input: obj(at2(httpbind.LocPayload, f("a", "A", blob())), at2(httpbind.LocPayload, f("b", "B", blob())))},
		"payload and body":             {Method: "PUT", URI: "/x", Input: obj(at2(httpbind.LocPayload, f("a", "A", blob())), f("b", "B", str()))},
		"response code in an input":    {Method: "PUT", URI: "/x", Input: obj(at2(httpbind.LocResponseCode, f("a", "A", num())))},
		"query in an output":           {Method: "PUT", URI: "/x", Output: obj(at2(httpbind.LocQuery, f("a", "A", str())))},
		"query struct":                 {Method: "GET", URI: "/x", Input: obj(at2(httpbind.LocQuery, f("a", "A", obj())))},
		"two members one query name":   {Method: "GET", URI: "/x", Input: obj(at2(httpbind.LocQuery, f("a", "A", str())), at2(httpbind.LocQuery, f("b", "A", str())))},
		"literal query repeated":       {Method: "GET", URI: "/x?A=1", Input: obj(at2(httpbind.LocQuery, f("a", "A", str())))},
		"nested place":                 {Method: "POST", URI: "/x", Input: obj(f("a", "A", obj(at2(httpbind.LocQuery, f("b", "B", str())))))},
		"token that is not a string":   {Method: "POST", URI: "/x", Input: obj(field{Key: "t", Name: "T", Shape: num(), IdempotencyToken: true})},
		"empty union":                  {Method: "POST", URI: "/x", Input: obj(f("a", "A", &shape{Kind: httpbind.KindStruct, Union: true}))},
		"payload in a HEAD answer":     {Method: "HEAD", URI: "/x", Output: obj(at2(httpbind.LocPayload, f("a", "A", blob())))},
	}
	for name, o := range cases {
		if err := o.Validate(); !errors.Is(err, httpbind.ErrShape) {
			t.Errorf("%s: err = %v, want ErrShape", name, err)
		}
		if _, err := o.Encode(restjson.Codec{}, nil, nil); !errors.Is(err, httpbind.ErrShape) {
			t.Errorf("%s: Encode err = %v, want ErrShape", name, err)
		}
	}
	// A greedy label is fine at the end, and a literal query pair stays in the query.
	ok := &httpbind.Operation{Method: "GET", URI: "/x/{K+}?x-id=Get", Input: obj(at2(httpbind.LocLabel, f("k", "K", str())))}
	if req := encode(t, ok, map[string]any{"k": "a/b"}); req.RawQuery != "x-id=Get" || req.RawPath != "/x/a/b" {
		t.Errorf("request = %+v", req)
	}
}

func nan() float64 { var z float64; return z / z }

var _ = http.MethodGet

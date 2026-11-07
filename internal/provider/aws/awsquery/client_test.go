package awsquery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/sigv4"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) Do(r *http.Request) (*http.Response, error) { return f(r) }

func answer(status int, body string, kv ...string) roundTrip {
	return func(*http.Request) (*http.Response, error) {
		h := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
}

type identity struct {
	Arn     string `xml:"Arn"`
	UserID  string `xml:"UserId"`
	Account string `xml:"Account"`
}

type fakeSigner struct {
	err  error
	body string
}

func (f *fakeSigner) Sign(req *http.Request, body []byte) error {
	f.body = string(body)
	req.Header.Set("Authorization", "signed")
	return f.err
}

func client(do Doer, s Signer) *Client {
	return &Client{HTTP: do, Signer: s, URL: "https://sts.eu-central-1.amazonaws.com", UserAgent: "qatlas/test"}
}

var identityCall = Call{Action: "GetCallerIdentity", Version: "2011-06-15"}

const identityXML = `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetCallerIdentityResult>
    <Arn>arn:aws:iam::123456789012:user/Alice</Arn>
    <UserId>AIDACKCEVSQ6C2EXAMPLE</UserId>
    <Account>123456789012</Account>
  </GetCallerIdentityResult>
  <ResponseMetadata><RequestId>01234567-89ab-cdef-0123-456789abcdef</RequestId></ResponseMetadata>
</GetCallerIdentityResponse>`

func TestEncodeBody(t *testing.T) {
	got, err := EncodeBody("GetAccessKeyInfo", "2011-06-15", map[string]string{"AccessKeyId": "AKIA 1&x", "B": "é"})
	if err != nil {
		t.Fatal(err)
	}
	want := "Action=GetAccessKeyInfo&Version=2011-06-15&AccessKeyId=AKIA+1%26x&B=%C3%A9"
	if string(got) != want {
		t.Fatalf("body = %s", got)
	}
	// Flattened names a later list encoder produces pass through.
	got, _ = EncodeBody("A", "2011-06-15", map[string]string{"Tags.member.1.Key": "k"})
	if string(got) != "Action=A&Version=2011-06-15&Tags.member.1.Key=k" {
		t.Fatalf("flattened body = %s", got)
	}
}

func TestEncodeBodyRefusesBadInput(t *testing.T) {
	big := map[string]string{"P": strings.Repeat("x", MaxBodySize)}
	tests := []struct {
		action, version string
		params          map[string]string
		want            error
	}{
		{"", "2011-06-15", nil, ErrAction},
		{"Get&Action", "2011-06-15", nil, ErrAction},
		{"A", "v1", nil, ErrVersion},
		{"A", "2011-06-15", map[string]string{"Action": "x"}, ErrParam},
		{"A", "2011-06-15", map[string]string{"Version": "x"}, ErrParam},
		{"A", "2011-06-15", map[string]string{"a=b": "x"}, ErrParam},
		{"A", "2011-06-15", map[string]string{"": "x"}, ErrParam},
		{"A", "2011-06-15", big, ErrTooLarge},
	}
	for _, test := range tests {
		if _, err := EncodeBody(test.action, test.version, test.params); !errors.Is(err, test.want) {
			t.Errorf("EncodeBody(%q,%q) = %v, want %v", test.action, test.version, err, test.want)
		}
	}
}

func TestDoDecodesResult(t *testing.T) {
	signer := &fakeSigner{}
	var seen *http.Request
	c := client(roundTrip(func(r *http.Request) (*http.Response, error) {
		seen = r
		return answer(200, identityXML)(r)
	}), signer)
	var out identity
	meta, err := c.Do(context.Background(), "aws.sts.caller-identity.get", identityCall, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out != (identity{"arn:aws:iam::123456789012:user/Alice", "AIDACKCEVSQ6C2EXAMPLE", "123456789012"}) {
		t.Fatalf("out = %+v", out)
	}
	if meta.RequestID != "01234567-89ab-cdef-0123-456789abcdef" || meta.RetryAfter != 0 {
		t.Fatalf("meta = %+v", meta)
	}
	if seen.Method != "POST" || seen.URL.String() != "https://sts.eu-central-1.amazonaws.com" ||
		seen.Header.Get("Content-Type") != ContentType || seen.Header.Get("User-Agent") != "qatlas/test" ||
		seen.Header.Get("Authorization") != "signed" {
		t.Fatalf("request = %+v %v", seen, seen.Header)
	}
	if signer.body != "Action=GetCallerIdentity&Version=2011-06-15" {
		t.Fatalf("signed body = %q", signer.body)
	}
	sent, _ := io.ReadAll(seen.Body)
	if string(sent) != signer.body {
		t.Fatalf("sent body %q differs from signed body %q", sent, signer.body)
	}
}

func TestDoSignsWithSigV4(t *testing.T) {
	signer := &sigv4.Signer{
		Credentials: sigv4.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"},
		Region:      "eu-central-1", Service: "sts",
	}
	var auth string
	c := client(roundTrip(func(r *http.Request) (*http.Response, error) {
		auth = r.Header.Get("Authorization")
		return answer(200, identityXML)(r)
	}), signer)
	if _, err := c.Do(context.Background(), "op", identityCall, &identity{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(auth, "/eu-central-1/sts/aws4_request, SignedHeaders=content-type;host;x-amz-date, Signature=") {
		t.Fatalf("Authorization = %s", auth)
	}
}

func TestDoWithoutResultElementOrOut(t *testing.T) {
	empty := `<DeleteThingResponse><ResponseMetadata><RequestId>abc-1</RequestId></ResponseMetadata></DeleteThingResponse>`
	meta, err := client(answer(200, empty), &fakeSigner{}).Do(context.Background(), "op", Call{Action: "DeleteThing", Version: "2011-06-15"}, nil)
	if err != nil || meta.RequestID != "abc-1" {
		t.Fatalf("meta %+v err %v", meta, err)
	}
	if _, err := client(answer(200, identityXML), &fakeSigner{}).Do(context.Background(), "op", identityCall, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDoErrorShapes(t *testing.T) {
	errBody := func(code string) string {
		return `<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><Error><Type>Sender</Type><Code>` + code +
			`</Code><Message>SECRET-PROVIDER-TEXT AKIAEXAMPLE</Message></Error><RequestId>req-42</RequestId></ErrorResponse>`
	}
	tests := []struct {
		name  string
		do    roundTrip
		class provider.Class
		retry time.Duration
	}{
		{"access denied", answer(403, errBody("AccessDenied")), provider.ClassPermission, 0},
		{"signature", answer(403, errBody("SignatureDoesNotMatch")), provider.ClassAuth, 0},
		{"expired", answer(403, errBody("ExpiredToken")), provider.ClassAuth, 0},
		{"throttling with retry", answer(400, errBody("Throttling"), "Retry-After", "5"), provider.ClassRateLimited, 5 * time.Second},
		{"throttling without retry", answer(400, errBody("RequestLimitExceeded")), provider.ClassRateLimited, 0},
		{"429 html", answer(429, "<html>slow down</html>"), provider.ClassRateLimited, 0},
		{"500", answer(500, errBody("InternalFailure")), provider.ClassProviderError, 0},
		{"unreadable body", answer(503, "\x00\x01 not xml"), provider.ClassProviderError, 0},
		{"empty body 403", answer(403, ""), provider.ClassPermission, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			meta, err := client(test.do, &fakeSigner{}).Do(context.Background(), "aws.op", identityCall, &identity{})
			if err == nil || err.Class != test.class || meta.RetryAfter != test.retry {
				t.Fatalf("err %+v meta %+v; want class %s retry %v", err, meta, test.class, test.retry)
			}
			dump := err.Error()
			if err.Original != nil {
				dump += " " + strings.Join(entryText(err.Original), " ")
			}
			if strings.Contains(dump, "SECRET-PROVIDER-TEXT") || strings.Contains(dump, "AKIAEXAMPLE") {
				t.Fatalf("provider text leaked: %s", dump)
			}
		})
	}
	meta, _ := client(answer(403, errBody("AccessDenied")), &fakeSigner{}).Do(context.Background(), "op", identityCall, nil)
	if meta.RequestID != "req-42" {
		t.Fatalf("RequestID = %q", meta.RequestID)
	}
}

func entryText(o *provider.Original) []string {
	var s []string
	for _, e := range o.Entries {
		s = append(s, e.Type, e.Code, e.Path, e.Message)
	}
	return s
}

func TestDoInvalidResponses(t *testing.T) {
	limited := client(answer(200, identityXML), &fakeSigner{})
	limited.ResponseLimit = 50
	tests := []struct {
		name string
		c    *Client
	}{
		{"not xml", client(answer(200, "{json}"), &fakeSigner{})},
		{"empty", client(answer(200, ""), &fakeSigner{})},
		{"wrong root", client(answer(200, `<Other><GetCallerIdentityResult/></Other>`), &fakeSigner{})},
		{"error shape on 200", client(answer(200, `<ErrorResponse><Error><Code>X</Code></Error></ErrorResponse>`), &fakeSigner{})},
		{"truncated", client(answer(200, identityXML[:120]), &fakeSigner{})},
		{"over the limit", limited},
	}
	for _, test := range tests {
		_, err := test.c.Do(context.Background(), "op", identityCall, &identity{})
		if err == nil || err.Class != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %+v", test.name, err)
		}
	}
}

func TestDoLimitBoundary(t *testing.T) {
	c := client(answer(200, identityXML), &fakeSigner{})
	c.ResponseLimit = int64(len(identityXML))
	if _, err := c.Do(context.Background(), "op", identityCall, &identity{}); err != nil {
		t.Fatalf("a body exactly at the limit failed: %v", err)
	}
}

func TestDoTransportAndInputFailures(t *testing.T) {
	timeout := roundTrip(func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded })
	if _, err := client(timeout, &fakeSigner{}).Do(context.Background(), "op", identityCall, nil); err == nil || err.Class != provider.ClassTimeout {
		t.Errorf("timeout: %+v", err)
	}
	refused := roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("dial tcp: secret-host") })
	if _, err := client(refused, &fakeSigner{}).Do(context.Background(), "op", identityCall, nil); err == nil ||
		err.Class != provider.ClassUnreachable || strings.Contains(err.Error(), "secret-host") {
		t.Errorf("unreachable: %+v", err)
	}
	called := false
	never := roundTrip(func(*http.Request) (*http.Response, error) { called = true; return nil, nil })
	for _, u := range []string{"http://sts.eu-central-1.amazonaws.com", "", "sts.amazonaws.com", "https://"} {
		c := client(never, &fakeSigner{})
		c.URL = u
		if _, err := c.Do(context.Background(), "op", identityCall, nil); err == nil {
			t.Errorf("URL %q accepted", u)
		}
	}
	if _, err := client(never, &fakeSigner{}).Do(context.Background(), "op", Call{Action: "bad action", Version: "2011-06-15"}, nil); err == nil {
		t.Error("bad action accepted")
	}
	signFail := &fakeSigner{err: errors.New("wJalrXUtnFEMI secret")}
	if _, err := client(never, signFail).Do(context.Background(), "op", identityCall, nil); err == nil || strings.Contains(err.Error(), "wJalr") {
		t.Errorf("sign failure: %+v", err)
	}
	if called {
		t.Error("a request was sent although input or signing failed")
	}
}

func TestResultDecodingIgnoresUnknownElementsAndNamespace(t *testing.T) {
	body := `<?xml version="1.0"?><GetCallerIdentityResponse xmlns="x"><Extra><Deep/></Extra>
		<GetCallerIdentityResult><Account>1</Account><New>y</New></GetCallerIdentityResult></GetCallerIdentityResponse>`
	var out identity
	if _, err := client(answer(200, body), &fakeSigner{}).Do(context.Background(), "op", identityCall, &out); err != nil || out.Account != "1" {
		t.Fatalf("out %+v err %v", out, err)
	}
}

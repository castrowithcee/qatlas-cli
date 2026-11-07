package aws

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

// otherKeyID is the second documented AWS example key ID, split for secret scanners.
const otherKeyID = "AKIA" + "I44QH8DHBEXAMPLE"

func decodeResult(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("result is not JSON: %v: %s", err, text)
	}
	return out
}

func TestCallerIdentity(t *testing.T) {
	f := direct(nil)
	e := newEnv(t, f, baseValues())
	text, err := e.invoke("aws.sts.caller-identity.get", `{}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	got := decodeResult(t, text)
	if got["account"] != testAccount || got["user_id"] != "AIDACKCEVSQ6C2EXAMPLE" || got["region"] != testRegion ||
		got["arn"] != "arn:aws:iam::123456789012:user/Alice" {
		t.Errorf("result = %v", got)
	}
	// The identity call is the account check as well: one request, not two.
	if len(f.requests) != 1 {
		t.Fatalf("requests = %v, want exactly one GetCallerIdentity", f.actions())
	}
	r := f.requests[0]
	if r.host != "sts.eu-central-1.amazonaws.com" || r.form.Get("Version") != "2011-06-15" ||
		r.header.Get("User-Agent") != "qatlas/1.2.3" || r.header.Get("X-Amz-Security-Token") != "" ||
		!strings.Contains(r.header.Get("Authorization"), "Credential="+testKeyID+"/20261106/eu-central-1/sts/aws4_request") {
		t.Errorf("request = host %q, form %v, header %v", r.host, r.form, r.header)
	}
}

func TestSessionTokenIsSigned(t *testing.T) {
	f := direct(nil)
	e := newEnv(t, f, baseValues().with(envToken, testToken))
	if _, err := e.invoke("aws.sts.caller-identity.get", `{}`); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	r := f.requests[0]
	if r.header.Get("X-Amz-Security-Token") != testToken || !strings.Contains(r.header.Get("Authorization"), "x-amz-security-token") {
		t.Errorf("the session token is not sent and signed: %v", r.header)
	}
	mustNotLeak(t, "the request line", r.body+r.host)
}

func TestAccessKeyInfoChecksTheAccountOncePerCredential(t *testing.T) {
	f := direct(func(r sent, _ int) reply {
		if r.action != "GetAccessKeyInfo" {
			return reply{status: 500}
		}
		return ok(`<GetAccessKeyInfoResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetAccessKeyInfoResult>` +
			`<Account>210987654321</Account></GetAccessKeyInfoResult></GetAccessKeyInfoResponse>`)
	})
	e := newEnv(t, f, baseValues())
	for i := 0; i < 2; i++ {
		text, err := e.invoke("aws.sts.access-key.get-info", `{"access_key_id":"`+otherKeyID+`"}`)
		if err != nil {
			t.Fatalf("invoke %d: %v", i, err)
		}
		if got := decodeResult(t, text); got["account"] != "210987654321" || got["region"] != testRegion {
			t.Errorf("result = %v", got)
		}
	}
	if f.count("GetCallerIdentity") != 1 || f.count("GetAccessKeyInfo") != 2 {
		t.Errorf("requests = %v, want one account check and two lookups", f.actions())
	}
	var lookup sent
	for _, r := range f.requests {
		if r.action == "GetAccessKeyInfo" {
			lookup = r
		}
	}
	if lookup.form.Get("AccessKeyId") != otherKeyID {
		t.Errorf("AccessKeyId = %q", lookup.form.Get("AccessKeyId"))
	}
	// The looked-up key is an argument, so it is not a secret of the connection.
	if strings.Contains(lookup.header.Get("Authorization"), otherKeyID) {
		t.Error("the looked-up key signed the request")
	}
}

func TestAccessKeyInfoArgumentIsChecked(t *testing.T) {
	for _, bad := range []string{`{}`, `{"access_key_id":""}`, `{"access_key_id":"short"}`,
		`{"access_key_id":"AKIA IOSFODNN7EXAMPLE"}`, `{"access_key_id":"` + testKeyID + `","extra":1}`} {
		f := direct(nil)
		e := newEnv(t, f, baseValues())
		if _, err := e.invoke("aws.sts.access-key.get-info", bad); err == nil || len(f.requests) != 0 {
			t.Errorf("%s: err = %v, requests = %v, want a refusal before any request", bad, err, f.actions())
		}
	}
}

func TestDecodeAuthorizationMessage(t *testing.T) {
	long := strings.Repeat("é", maxDecodedBytes) // two bytes each, so the cut falls on a rune boundary
	f := direct(func(r sent, _ int) reply {
		if r.action != "DecodeAuthorizationMessage" {
			return reply{status: 500}
		}
		message := `{"allowed":false}`
		if r.form.Get("EncodedMessage") == "long" {
			message = long
		}
		return ok(`<DecodeAuthorizationMessageResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">` +
			`<DecodeAuthorizationMessageResult><DecodedMessage>` + message + `</DecodedMessage>` +
			`</DecodeAuthorizationMessageResult></DecodeAuthorizationMessageResponse>`)
	})
	e := newEnv(t, f, baseValues())
	text, err := e.invoke("aws.sts.authorization-message.decode", `{"encoded_message":"short"}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if got := decodeResult(t, text); got["decoded_message"] != `{"allowed":false}` || got["truncated"] != false {
		t.Errorf("result = %v", got)
	}
	text, err = e.invoke("aws.sts.authorization-message.decode", `{"encoded_message":"long"}`)
	if err != nil {
		t.Fatalf("invoke long: %v", err)
	}
	got := decodeResult(t, text)
	decoded, _ := got["decoded_message"].(string)
	if got["truncated"] != true || len(decoded) > maxDecodedBytes || len(decoded) < maxDecodedBytes-1 {
		t.Errorf("long message: %d bytes, truncated=%v", len(decoded), got["truncated"])
	}
	for _, bad := range []string{`{}`, `{"encoded_message":""}`, `{"encoded_message":"` + strings.Repeat("a", 10241) + `"}`} {
		if _, err := e.invoke("aws.sts.authorization-message.decode", bad); err == nil {
			t.Errorf("%.40s accepted", bad)
		}
	}
}

// A failure message of the service is a provider error of the generic class; its text never reaches us.
func TestDecodeFailureKeepsProviderTextOut(t *testing.T) {
	f := direct(func(sent, int) reply {
		return reply{status: 400, body: errorXML("InvalidAuthorizationMessageException")}
	})
	e := newEnv(t, f, baseValues())
	_, err := e.invoke("aws.sts.authorization-message.decode", `{"encoded_message":"x"}`)
	if classOf(err) != provider.ClassProviderError {
		t.Fatalf("err = %v, want a provider error", err)
	}
	mustNotLeak(t, "the error", err.Error())
	if strings.Contains(err.Error(), "provider text") {
		t.Errorf("the provider's Message reached the error: %v", err)
	}
	var failed *provider.Error
	if errorsAs(err, &failed); failed.Original == nil || len(failed.Original.Entries) != 1 ||
		failed.Original.Entries[0].Code != "InvalidAuthorizationMessageException" || failed.Original.Entries[0].Message != "" {
		t.Errorf("original = %+v", failed.Original)
	}
}

func TestAccountMismatchRefusesTheCall(t *testing.T) {
	other := &fakeAWS{answer: func(r sent, _ int) reply {
		if r.action == "GetCallerIdentity" {
			return ok(identityFor("999999999999"))
		}
		return ok(`<GetAccessKeyInfoResponse><GetAccessKeyInfoResult><Account>1</Account></GetAccessKeyInfoResult></GetAccessKeyInfoResponse>`)
	}}
	e := newEnv(t, other, baseValues())
	for _, tool := range [][2]string{
		{"aws.sts.caller-identity.get", `{}`},
		{"aws.sts.access-key.get-info", `{"access_key_id":"` + testKeyID + `"}`},
		{"aws.sts.authorization-message.decode", `{"encoded_message":"x"}`},
	} {
		text, err := e.invoke(tool[0], tool[1])
		var failed *provider.Error
		if !errorsAs(err, &failed) || failed.Class != provider.ClassPermission || failed.Message != mismatchMessage {
			t.Errorf("%s: err = %v, want the account refusal", tool[0], err)
		}
		if text != "" || strings.Contains(err.Error(), "999999999999") || strings.Contains(err.Error(), testAccount) {
			t.Errorf("%s: the refusal leaks a result or an account ID: %q %v", tool[0], text, err)
		}
	}
	// Nothing but the check ever reached AWS, and a refusal is not remembered: every call checks again.
	if other.count("GetAccessKeyInfo")+other.count("DecodeAuthorizationMessage") != 0 || other.count("GetCallerIdentity") != 3 {
		t.Errorf("requests = %v", other.actions())
	}
}

func TestRegionOutsideTheAllowlistIsRefusedBeforeAnyIO(t *testing.T) {
	f := direct(nil)
	// An empty secret store proves that not even a secret was read: the region is judged first.
	e := newEnv(t, f, values{}, "account/"+testAccount, "region/eu-central-1", "region/us-east-1")
	for _, args := range []string{`{"region":"eu-west-1"}`, `{"region":"cn-north-1"}`, `{"region":"us-gov-west-1"}`, `{}`} {
		_, err := e.invoke("aws.sts.caller-identity.get", args)
		if !isInvalidRequest(err) || len(f.requests) != 0 {
			t.Errorf("%s: err = %v, requests = %v, want an invalid request and no I/O", args, err, f.actions())
		}
	}
	for _, args := range []string{`{"region":"EU-WEST-1"}`, `{"region":"../x"}`, `{"region":"eu-west-1.evil"}`, `{"region":7}`} {
		if _, err := e.invoke("aws.sts.caller-identity.get", args); err == nil || len(f.requests) != 0 {
			t.Errorf("%s: err = %v, requests = %v", args, err, f.actions())
		}
	}
}

func TestRegionSelectsTheEndpoint(t *testing.T) {
	f := direct(nil)
	e := newEnv(t, f, baseValues(), "account/"+testAccount, "region/eu-central-1", "region/us-east-1")
	text, err := e.invoke("aws.sts.caller-identity.get", `{"region":"us-east-1"}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if got := decodeResult(t, text); got["region"] != "us-east-1" ||
		f.requests[0].host != "sts.us-east-1.amazonaws.com" ||
		!strings.Contains(f.requests[0].header.Get("Authorization"), "/us-east-1/sts/aws4_request") {
		t.Errorf("result = %v, host = %q", got, f.requests[0].host)
	}
}

func TestInvalidTargetsRefuseTheCall(t *testing.T) {
	for _, targets := range [][]string{
		{"account/" + testAccount}, {"region/eu-central-1"}, {"account/" + testAccount, "region/cn-north-1"},
		{"account/" + testAccount, "account/210987654321", "region/eu-central-1"},
	} {
		f := direct(nil)
		e := newEnv(t, f, baseValues(), targets...)
		if _, err := e.invoke("aws.sts.caller-identity.get", `{}`); err == nil || len(f.requests) != 0 {
			t.Errorf("targets %v: err = %v, requests = %v, want a refusal before any request", targets, err, f.actions())
		}
	}
}

package aws

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

// roleAWS answers like STS with a role: AssumeRole issues the session, and GetCallerIdentity answers the
// account of whichever credential signed it.
func roleAWS(clock *testClock, lifetime time.Duration, sessionAccount string) *fakeAWS {
	return &fakeAWS{answer: func(r sent, n int) reply {
		switch r.action {
		case "AssumeRole":
			return ok(assumeNumbered(clock.Now().Add(lifetime), n))
		case "GetAccessKeyInfo":
			return ok(`<GetAccessKeyInfoResponse><GetAccessKeyInfoResult><Account>210987654321</Account></GetAccessKeyInfoResult></GetAccessKeyInfoResponse>`)
		case "GetCallerIdentity":
			return ok(identityFor(sessionAccount))
		}
		return reply{status: 500}
	}}
}

func roleEnv(t *testing.T, lifetime time.Duration, sessionAccount string) (*env, *fakeAWS, *testClock) {
	t.Helper()
	f := &fakeAWS{}
	e := newEnv(t, f, roleValues())
	clock := clockOf(t)
	*f = *roleAWS(clock, lifetime, sessionAccount)
	return e, f, clock
}

// clockOf returns the clock install put in place.
func clockOf(t *testing.T) *testClock {
	t.Helper()
	c, ok := clock.(*testClock)
	if !ok {
		t.Fatal("the test clock is not installed")
	}
	return c
}

func TestRoleIsAssumedAndTheSessionSigns(t *testing.T) {
	e, f, _ := roleEnv(t, time.Hour, testAccount)
	text, err := e.invoke("aws.sts.caller-identity.get", `{}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if got := decodeResult(t, text); got["account"] != testAccount {
		t.Errorf("result = %v", got)
	}
	if actions := f.actions(); len(actions) != 2 || actions[0] != "AssumeRole" || actions[1] != "GetCallerIdentity" {
		t.Fatalf("requests = %v, want AssumeRole and then GetCallerIdentity", actions)
	}
	assume, identity := f.requests[0], f.requests[1]
	if assume.form.Get("RoleArn") != testRoleARN || assume.form.Get("DurationSeconds") != "3600" ||
		assume.form.Get("ExternalId") != testExternal || assume.form.Get("RoleSessionName") != sessionName("prod") ||
		!sessionNameForm.MatchString(assume.form.Get("RoleSessionName")) {
		t.Errorf("AssumeRole form = %v", assume.form)
	}
	// The role is assumed with the access key; the answer is signed with the session.
	if !strings.Contains(assume.header.Get("Authorization"), "Credential="+testKeyID+"/") || assume.header.Get("X-Amz-Security-Token") != "" ||
		assume.header.Get("User-Agent") != "qatlas/1.2.3" {
		t.Errorf("AssumeRole header = %v", assume.header)
	}
	if !strings.Contains(identity.header.Get("Authorization"), "Credential="+sessionKeyID+"/") ||
		identity.header.Get("X-Amz-Security-Token") != sessionToken+"1" || identity.header.Get("User-Agent") != "qatlas/1.2.3" {
		t.Errorf("identity header = %v", identity.header)
	}
	mustNotLeak(t, "the result", text)
}

func TestSessionIsReusedAndRenewedShortlyBeforeExpiry(t *testing.T) {
	e, f, clock := roleEnv(t, time.Hour, testAccount)
	call := func() {
		t.Helper()
		if _, err := e.invoke("aws.sts.access-key.get-info", `{"access_key_id":"`+testKeyID+`"}`); err != nil {
			t.Fatal(err)
		}
	}
	call()
	call()
	if f.count("AssumeRole") != 1 || f.count("GetCallerIdentity") != 1 {
		t.Fatalf("after two calls: %v, want one session and one account check", f.actions())
	}
	clock.advance(54 * time.Minute) // six minutes left: still valid
	call()
	if f.count("AssumeRole") != 1 {
		t.Errorf("a session with six minutes left was replaced: %v", f.actions())
	}
	clock.advance(2 * time.Minute) // four minutes left: inside the renewal window
	call()
	if f.count("AssumeRole") != 2 || f.count("GetCallerIdentity") != 2 {
		t.Errorf("after renewal: %v, want a second session and a second account check", f.actions())
	}
}

func TestSessionsAreKeptPerRegion(t *testing.T) {
	f := &fakeAWS{}
	e := newEnv(t, f, roleValues(), "account/"+testAccount, "region/eu-central-1", "region/us-east-1")
	*f = *roleAWS(clockOf(t), time.Hour, testAccount)
	for _, region := range []string{"eu-central-1", "us-east-1", "eu-central-1"} {
		if _, err := e.invoke("aws.sts.caller-identity.get", `{"region":"`+region+`"}`); err != nil {
			t.Fatalf("%s: %v", region, err)
		}
	}
	if f.count("AssumeRole") != 2 {
		t.Errorf("requests = %v, want one session per region", f.actions())
	}
	if f.requests[0].host != "sts.eu-central-1.amazonaws.com" || f.requests[2].host != "sts.us-east-1.amazonaws.com" {
		t.Errorf("the role was not assumed at the endpoint of the region: %q %q", f.requests[0].host, f.requests[2].host)
	}
}

func TestConcurrentCallsShareOneAssumeRole(t *testing.T) {
	_, f, _ := roleEnv(t, time.Hour, testAccount)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			red := &redact.Redactor{}
			client, err := open(context.Background(), connection(), resolverFor(roleValues(), red), red)
			if err != nil {
				t.Errorf("open: %v", err)
				return
			}
			if _, err := client.authorize(context.Background(), "test", testRegion); err != nil {
				t.Errorf("authorize: %v", err)
			}
		}()
	}
	wg.Wait()
	if f.count("AssumeRole") != 1 {
		t.Errorf("AssumeRole ran %d times, want once", f.count("AssumeRole"))
	}
}

func TestAssumeRoleFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reply  reply
		class  provider.Class
		phrase string
	}{
		{"access denied", reply{status: 403, body: errorXML("AccessDenied")}, provider.ClassPermission, "multi-factor"},
		{"an expired key", reply{status: 403, body: errorXML("ExpiredToken")}, provider.ClassAuth, "expired"},
		{"an unknown key", reply{status: 403, body: errorXML("InvalidClientTokenId")}, provider.ClassAuth, "access key"},
		{"a bad signature", reply{status: 403, body: errorXML("SignatureDoesNotMatch")}, provider.ClassAuth, "secret access key"},
		{"a disabled region", reply{status: 403, body: errorXML("RegionDisabledException")}, provider.ClassPermission, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeAWS{answer: func(sent, int) reply { return tc.reply }}
			e := newEnv(t, f, roleValues())
			_, err := e.invoke("aws.sts.caller-identity.get", `{}`)
			var failed *provider.Error
			if !errorsAs(err, &failed) || (tc.class == provider.ClassPermission && failed.Class != provider.ClassPermission &&
				tc.name != "a disabled region") || (tc.name != "a disabled region" && failed.Class != tc.class) {
				t.Fatalf("err = %v, want class %s", err, tc.class)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.phrase) {
				t.Errorf("message %q lacks %q", err.Error(), tc.phrase)
			}
			// Nothing was called with a session that does not exist.
			if f.count("GetCallerIdentity") != 0 || f.count("AssumeRole") != 1 {
				t.Errorf("requests = %v", f.actions())
			}
			mustNotLeak(t, "the error", err.Error(), testKeyID)
			if strings.Contains(err.Error(), "provider text") {
				t.Errorf("provider text reached the error: %v", err)
			}
		})
	}
}

func TestMFARoleMessageIsClear(t *testing.T) {
	f := &fakeAWS{answer: func(sent, int) reply { return reply{status: 403, body: errorXML("AccessDenied")} }}
	e := newEnv(t, f, roleValues())
	_, err := e.invoke("aws.sts.caller-identity.get", `{}`)
	for _, want := range []string{"trust policy", "sts:AssumeRole", "external ID", "multi-factor authentication cannot be assumed"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("message %v lacks %q", err, want)
		}
	}
	// A refusal is retried at the next call: it is not cached.
	_, _ = e.invoke("aws.sts.caller-identity.get", `{}`)
	if f.count("AssumeRole") != 2 {
		t.Errorf("a refused assumption was cached: %v", f.actions())
	}
}

func TestRoleOfAnotherAccountIsRefusedBeforeAnyRequest(t *testing.T) {
	f := direct(nil)
	e := newEnv(t, f, roleValues().with(envRole, "arn:aws:iam::999999999999:role/Qatlas"))
	_, err := e.invoke("aws.sts.caller-identity.get", `{}`)
	var failed *provider.Error
	if !errorsAs(err, &failed) || failed.Message != mismatchMessage || len(f.requests) != 0 {
		t.Errorf("err = %v, requests = %v", err, f.actions())
	}
}

func TestSessionOfAnotherAccountIsRefused(t *testing.T) {
	e, f, _ := roleEnv(t, time.Hour, "999999999999")
	_, err := e.invoke("aws.sts.access-key.get-info", `{"access_key_id":"`+testKeyID+`"}`)
	var failed *provider.Error
	if !errorsAs(err, &failed) || failed.Message != mismatchMessage || f.count("GetAccessKeyInfo") != 0 {
		t.Errorf("err = %v, requests = %v", err, f.actions())
	}
}

func TestUnusableSessionAnswersAreRefused(t *testing.T) {
	for name, body := range map[string]string{
		"no expiration":    strings.Replace(assumeXML(testNow.Add(time.Hour)), "<Expiration>"+testNow.Add(time.Hour).Format(time.RFC3339)+"</Expiration>", "", 1),
		"a bad expiration": strings.Replace(assumeXML(testNow.Add(time.Hour)), testNow.Add(time.Hour).Format(time.RFC3339), "tomorrow", 1),
		"no token":         strings.Replace(assumeXML(testNow.Add(time.Hour)), sessionToken, "", 1),
		"an old session":   assumeXML(testNow.Add(2 * time.Minute)),
		"an expired one":   assumeXML(testNow.Add(-time.Hour)),
	} {
		f := &fakeAWS{answer: func(sent, int) reply { return ok(body) }}
		e := newEnv(t, f, roleValues())
		if _, err := e.invoke("aws.sts.caller-identity.get", `{}`); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v, want an invalid response", name, err)
		}
		if f.count("AssumeRole") != 1 {
			t.Errorf("%s: requests = %v, want no loop", name, f.actions())
		}
	}
}

func TestExternalIDNeedsARole(t *testing.T) {
	f := direct(nil)
	v := baseValues().with(envExternal, testExternal)
	e := newEnv(t, f, v)
	_, err := e.invoke("aws.sts.caller-identity.get", `{}`)
	if classOf(err) != provider.ClassAuth || len(f.requests) != 0 {
		t.Errorf("err = %v, requests = %v", err, f.actions())
	}
	mustNotLeak(t, "the error", err.Error())
}

func TestUnusableCredentialValuesAreRefusedBeforeAnyRequest(t *testing.T) {
	for name, v := range map[string]values{
		"a short key ID":          baseValues().with(envKeyID, "AKIA"),
		"a key ID with space":     baseValues().with(envKeyID, "AKIAIOSFODNN7 EXAMPL"),
		"a short secret":          baseValues().with(envSecret, "short"),
		"a secret with a newline": baseValues().with(envSecret, testSecret+"\n"),
		"a bad session token":     baseValues().with(envToken, "bad token with spaces"),
		"a cn role":               roleValues().with(envRole, "arn:aws-cn:iam::123456789012:role/Qatlas"),
		"a user ARN":              roleValues().with(envRole, "arn:aws:iam::123456789012:user/Alice"),
		"a bad external ID":       roleValues().with(envExternal, "bad external id"),
		"a one-char external":     roleValues().with(envExternal, "x"),
	} {
		f := direct(nil)
		e := newEnv(t, f, v)
		_, err := e.invoke("aws.sts.caller-identity.get", `{}`)
		if err == nil || len(f.requests) != 0 {
			t.Errorf("%s: err = %v, requests = %v", name, err, f.actions())
			continue
		}
		mustNotLeak(t, name, err.Error())
	}
}

// No credential of the connection or of its session reaches a result, an error, or an unredacted text.
func TestCredentialsNeverLeak(t *testing.T) {
	e, f, _ := roleEnv(t, time.Hour, testAccount)
	text, err := e.invoke("aws.sts.caller-identity.get", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	mustNotLeak(t, "the result", text)
	for _, r := range f.requests {
		// The secret never travels in a request, only its signature. The external ID does: AWS reads it from
		// the AssumeRole body.
		for _, value := range []string{testSecret, testToken, sessionSecret, sessionToken} {
			if strings.Contains(r.body, value) {
				t.Errorf("a request body holds %.8s...", value)
			}
		}
		for name, values := range r.header {
			if name == "X-Amz-Security-Token" {
				continue
			}
			mustNotLeak(t, "the header "+name, strings.Join(values, ","))
		}
	}
	// Every credential of the connection and of the session is known to the redactor.
	for _, value := range []string{testKeyID, testSecret, testExternal, sessionKeyID, sessionSecret, sessionToken + "1"} {
		if got := e.red.Apply("x " + value + " y"); strings.Contains(got, value) {
			t.Errorf("the redactor does not know %.8s...", value)
		}
	}
	// A later invoke has a redactor of its own that never saw the fetch: the cached session is registered again.
	fresh := newRedactorFor(t, e)
	_, _ = e.invoke("aws.sts.caller-identity.get", `{}`)
	if got := fresh.Apply(sessionSecret + sessionToken + "1"); strings.Contains(got, sessionSecret) {
		t.Error("a reused session is not registered with the redactor")
	}
	out, _ := json.Marshal(f.actions())
	mustNotLeak(t, "the action log", string(out))
}

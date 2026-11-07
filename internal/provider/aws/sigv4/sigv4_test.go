package sigv4

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The vectors below come from the AWS Signature Version 4 test suite (aws-sig-v4-test-suite), read from its
// copy in boto/botocore tests/unit/auth/aws4_testsuite: example credentials, region us-east-1, service name
// "service", time 2015-08-30T12:36:00Z. Each want is the suite's .authz file.
const (
	exampleKey    = "AKIDEXAMPLE"
	exampleSecret = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
	exampleToken  = "AQoDYXdzEPT//////////wEXAMPLEtc764bNrC9SAPBSM22wDOk4x4HIZ8j4FZTwdQWLWsKWHGBuFqwAeMicRXmxfpSPfIeoIYRqTflfKD8YUuwthAx7mSEI/qkPpKPi/kMcGdQrmGdeehM4IC1NtBmUpp2wUE8phUZampKsburEDy0KPkyQDYwT7WZ0wq5VSXDvp75YU9HFvlRd8Tx6q6fE8YQcHNVXAkiY9q6d+xo0rKwT38xVqr7ZD0u0iPPkUL64lIZbqBAz+scqKmlzm8FDrypNC9Yjc8fPOLn9FX9KSYvKTr4rvx3iSIlTJabIQwj2ICCR/oLxBA=="
)

func suiteSigner(token string) *Signer {
	return &Signer{
		Credentials: Credentials{AccessKeyID: exampleKey, SecretAccessKey: exampleSecret, SessionToken: token},
		Region:      "us-east-1",
		Service:     "service",
		Clock: ClockFunc(func() time.Time {
			return time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
		}),
	}
}

func TestSuiteVectors(t *testing.T) {
	const scope = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, SignedHeaders="
	tests := []struct {
		name    string
		method  string
		target  string
		headers [][2]string
		body    string
		token   string
		want    string
	}{
		{"get-vanilla", "GET", "/", nil, "", "",
			scope + "host;x-amz-date, Signature=5fa00fa31553b73ebf1942676e86291e8372ff2a2260956d9b8aae1d763fbf31"},
		{"get-vanilla-empty-query-key", "GET", "/?Param1=value1", nil, "", "",
			scope + "host;x-amz-date, Signature=a67d582fa61cc504c4bae71f336f98b97f1ea3c7a6bfe1b6e45aec72011b9aeb"},
		{"get-vanilla-query-order-key", "GET", "/?Param1=value2&Param1=Value1", nil, "", "",
			scope + "host;x-amz-date, Signature=eedbc4e291e521cf13422ffca22be7d2eb8146eecf653089df300a15b2382bd1"},
		{"get-vanilla-query-order-key-case", "GET", "/?Param2=value2&Param1=value1", nil, "", "",
			scope + "host;x-amz-date, Signature=b97d918cfa904a5beff61c982a1b6f458b799221646efd99d3219ec94cdf2500"},
		{"get-header-value-trim", "GET", "/",
			[][2]string{{"My-Header1", " value1"}, {"My-Header2", `"a   b   c"`}}, "", "",
			scope + "host;my-header1;my-header2;x-amz-date, Signature=acc3ed3afb60bb290fc8d2dd0098b9911fcaa05412b367055dee359757a9c736"},
		{"post-x-www-form-urlencoded", "POST", "/",
			[][2]string{{"Content-Type", "application/x-www-form-urlencoded"}}, "Param1=value1", "",
			scope + "content-type;host;x-amz-date, Signature=ff11897932ad3f4e8b18135d722051e5ac45fc38421b1da7b9d196a0fe09473a"},
		{"post-sts-header-before", "POST", "/", nil, "", exampleToken,
			scope + "host;x-amz-date;x-amz-security-token, Signature=85d96828115b5dc0cfc3bd16ad9e210dd772bbebba041836c64533a82be05ead"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req, err := http.NewRequest(test.method, "https://example.amazonaws.com"+test.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, h := range test.headers {
				req.Header.Set(h[0], h[1])
			}
			if err := suiteSigner(test.token).Sign(req, []byte(test.body)); err != nil {
				t.Fatal(err)
			}
			if got := req.Header.Get("Authorization"); got != test.want {
				t.Fatalf("Authorization =\n%s\nwant\n%s", got, test.want)
			}
			if got := req.Header.Get("X-Amz-Date"); got != "20150830T123600Z" {
				t.Fatalf("X-Amz-Date = %q", got)
			}
			if got := req.Header.Get("X-Amz-Security-Token"); got != test.token {
				t.Fatalf("X-Amz-Security-Token = %q", got)
			}
		})
	}
}

func TestSigningKeyHasFourChainedSteps(t *testing.T) {
	// The key must depend on every scope part.
	base := fmt.Sprintf("%x", signingKey("s", "20150830", "us-east-1", "iam"))
	for name, other := range map[string][]byte{
		"secret":  signingKey("t", "20150830", "us-east-1", "iam"),
		"day":     signingKey("s", "20150831", "us-east-1", "iam"),
		"region":  signingKey("s", "20150830", "eu-west-1", "iam"),
		"service": signingKey("s", "20150830", "us-east-1", "sts"),
	} {
		if fmt.Sprintf("%x", other) == base {
			t.Errorf("signing key ignores %s", name)
		}
	}
}

func TestCanonicalPath(t *testing.T) {
	tests := []struct {
		name, target string
		s3           bool
		want         string
	}{
		{"root", "https://h/", false, "/"},
		{"empty", "https://h", false, "/"},
		{"double encoding", "https://h/documents%20and%20settings/", false, "/documents%2520and%2520settings/"},
		{"dot segments", "https://h/a/./b/../c//d/", false, "/a/c/d/"},
		{"s3 single encoding", "https://h/my%20key/a/../b", true, "/my%20key/a/../b"},
		{"s3 reserved", "https://h/a!b", true, "/a%21b"},
	}
	for _, test := range tests {
		req, err := http.NewRequest("GET", test.target, nil)
		if err != nil {
			t.Fatal(err)
		}
		s := &Signer{S3Paths: test.s3}
		if got := s.canonicalPath(req.URL); got != test.want {
			t.Errorf("%s: canonicalPath = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestCanonicalQuery(t *testing.T) {
	got, err := canonicalQuery("b=2&a=x+y&a=%7e&flag&c=%2F")
	if err != nil {
		t.Fatal(err)
	}
	if want := "a=x%20y&a=~&b=2&c=%2F&flag="; got != want {
		t.Fatalf("canonicalQuery = %q, want %q", got, want)
	}
	if _, err := canonicalQuery("a=%zz"); err != ErrBadQuery {
		t.Fatalf("malformed query error = %v", err)
	}
}

func TestPayloadOptions(t *testing.T) {
	sign := func(s *Signer, body string) *http.Request {
		t.Helper()
		req, err := http.NewRequest("PUT", "https://bucket.s3.eu-central-1.amazonaws.com/key", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Sign(req, []byte(body)); err != nil {
			t.Fatal(err)
		}
		return req
	}
	plain := suiteSigner("")
	if got := sign(plain, "x").Header.Get("X-Amz-Content-Sha256"); got != "" {
		t.Fatalf("header set without option: %q", got)
	}
	withHash := suiteSigner("")
	withHash.ContentSHA256Header = true
	req := sign(withHash, "")
	if got := req.Header.Get("X-Amz-Content-Sha256"); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("empty payload hash = %q", got)
	}
	if !strings.Contains(req.Header.Get("Authorization"), "SignedHeaders=host;x-amz-content-sha256;x-amz-date,") {
		t.Fatalf("content hash not signed: %s", req.Header.Get("Authorization"))
	}
	unsigned := suiteSigner("")
	unsigned.UnsignedPayload = true
	req = sign(unsigned, "ignored")
	if got := req.Header.Get("X-Amz-Content-Sha256"); got != UnsignedPayload {
		t.Fatalf("unsigned payload header = %q", got)
	}
	if req.Header.Get("Authorization") == sign(withHash, "ignored").Header.Get("Authorization") {
		t.Fatal("unsigned payload signs the body hash")
	}
}

func TestUnsignedHeadersAreNotSigned(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.amazonaws.com/", nil)
	req.Header.Set("User-Agent", "qatlas/test")
	req.Header.Set("Authorization", "stale")
	if err := suiteSigner("").Sign(req, nil); err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("Authorization"); !strings.Contains(got, "SignedHeaders=host;x-amz-date,") {
		t.Fatalf("Authorization = %s", got)
	}
}

func TestSignRefusesIncompleteInput(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://example.amazonaws.com/", nil)
	noKey := suiteSigner("")
	noKey.Credentials.SecretAccessKey = ""
	if err := noKey.Sign(req, nil); err != ErrNoCredentials {
		t.Errorf("empty secret: %v", err)
	}
	noScope := suiteSigner("")
	noScope.Service = ""
	if err := noScope.Sign(req, nil); err != ErrNoScope {
		t.Errorf("empty service: %v", err)
	}
	noHost, _ := http.NewRequest("GET", "/path", nil)
	if err := suiteSigner("").Sign(noHost, nil); err != ErrNoHost {
		t.Errorf("no host: %v", err)
	}
}

func TestSecretsStayOutOfFormattingAndErrors(t *testing.T) {
	s := suiteSigner(exampleToken)
	for _, text := range []string{
		fmt.Sprintf("%v %+v %#v %s", s.Credentials, s.Credentials, s.Credentials, s.Credentials),
		fmt.Sprintf("%v %+v %s", s, s, s),
		fmt.Sprint(ErrNoCredentials, ErrNoScope, ErrNoHost, ErrBadQuery),
	} {
		for _, secret := range []string{exampleSecret, exampleToken} {
			if strings.Contains(text, secret) {
				t.Fatalf("secret leaked into %q", text)
			}
		}
	}
	bad, _ := http.NewRequest("GET", "https://example.amazonaws.com/?a=%zz", nil)
	if err := s.Sign(bad, nil); err == nil || strings.Contains(err.Error(), exampleSecret) {
		t.Fatalf("bad query error = %v", err)
	}
}

func TestSystemClockIsUsedByDefault(t *testing.T) {
	s := suiteSigner("")
	s.Clock = nil
	req, _ := http.NewRequest("GET", "https://example.amazonaws.com/", nil)
	before := time.Now().UTC().Add(-time.Minute)
	if err := s.Sign(req, nil); err != nil {
		t.Fatal(err)
	}
	stamp, err := time.Parse(dateFormat, req.Header.Get("X-Amz-Date"))
	if err != nil || stamp.Before(before) {
		t.Fatalf("X-Amz-Date = %q (%v)", req.Header.Get("X-Amz-Date"), err)
	}
}

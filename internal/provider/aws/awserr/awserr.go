// Package awserr maps AWS error codes and HTTP statuses to provider error classes. It serves every AWS
// protocol (awsQuery now, the JSON and REST protocols later): the caller extracts the code, this package
// decides the class and the message. The provider's own Message text never reaches a message or a log.
package awserr

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

// Subject names the provider in messages.
const Subject = "AWS"

// rule is the Qatlas-owned outcome for one provider error code.
type rule struct {
	class   provider.Class
	message string
}

var (
	rateLimited = rule{provider.ClassRateLimited, Subject + " rate-limited the operation"}

	// codeRules is the table error code -> class. It is data: add a row, not a branch.
	codeRules = map[string]rule{
		"Throttling":            rateLimited,
		"ThrottlingException":   rateLimited,
		"RequestLimitExceeded":  rateLimited,
		"AccessDenied":          {provider.ClassPermission, Subject + " denied the operation; the identity lacks the required permission"},
		"InvalidClientTokenId":  {provider.ClassAuth, Subject + " does not recognise the access key"},
		"SignatureDoesNotMatch": {provider.ClassAuth, Subject + " rejected the request signature; check the secret access key"},
		"ExpiredToken":          {provider.ClassAuth, "the " + Subject + " session token has expired"},
	}
)

// safeToken limits what a provider-chosen code may be before it is kept in Error.Original.
var safeToken = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

// NormalizeCode reduces a protocol's raw error code to the bare code. JSON protocols prefix a namespace
// ("com.amazon.coral.service#ThrottlingException") and some append a URL after a colon.
func NormalizeCode(raw string) string {
	if i := strings.LastIndex(raw, "#"); i >= 0 {
		raw = raw[i+1:]
	}
	if i := strings.Index(raw, ":"); i >= 0 {
		raw = raw[:i]
	}
	return strings.TrimSpace(raw)
}

// Classify turns a failed answer into a provider error. code is the provider's error code as NormalizeCode
// returns it, or empty when the body had none. The returned duration is the Retry-After the provider sent
// for a rate-limited answer, zero otherwise; callers hold their limiter for it and bound it themselves.
// The error carries only the status and the code and type tokens in Original, never the provider's Message.
func Classify(op string, status int, header http.Header, code, errType string) (*provider.Error, time.Duration) {
	failure, hold := classify(op, status, header, code)
	failure.Original = original(status, code, errType)
	return failure, hold
}

func classify(op string, status int, header http.Header, code string) (*provider.Error, time.Duration) {
	if r, ok := codeRules[code]; ok {
		if r.class == provider.ClassRateLimited {
			return limited(op, header)
		}
		return &provider.Error{Class: r.class, Op: op, Message: r.message}, 0
	}
	switch {
	case status == http.StatusTooManyRequests:
		return limited(op, header)
	case status >= 500:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: Subject + " reported a server-side failure (HTTP " + strconv.Itoa(status) + ")"}, 0
	case status == http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: Subject + " rejected the credentials"}, 0
	case status == http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: Subject + " denied the operation; the identity lacks the required permission"}, 0
	}
	return &provider.Error{Class: provider.ClassProviderError, Op: op,
		Message: Subject + " rejected the operation (HTTP " + strconv.Itoa(status) + ")"}, 0
}

func limited(op string, header http.Header) (*provider.Error, time.Duration) {
	hold := provider.RetryAfter(header)
	message := rateLimited.message
	if hold > 0 {
		message += fmt.Sprintf("; retry after %d seconds", int((hold+time.Second-1)/time.Second))
	}
	return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: message}, hold
}

func original(status int, code, errType string) *provider.Original {
	o := &provider.Original{Status: status}
	entry := provider.OriginalEntry{}
	if safeToken.MatchString(code) {
		entry.Code = code
	}
	if safeToken.MatchString(errType) {
		entry.Type = errType
	}
	if entry != (provider.OriginalEntry{}) {
		o.Entries = []provider.OriginalEntry{entry}
	}
	return o
}

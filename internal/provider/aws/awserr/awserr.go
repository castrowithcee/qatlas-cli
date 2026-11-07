// Package awserr maps AWS error codes and HTTP statuses to provider error classes. It serves every AWS
// protocol (awsQuery and restJson1 today, restXml next): the caller extracts the code, this package decides
// the class and the message. The provider's own Message text never reaches a message or a log.
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
	notFound    = rule{provider.ClassNotFound, Subject + " did not find the entity; check the name and whether the credential may see it"}
	conflict    = rule{provider.ClassProviderError, Subject + " reports a conflict: the entity already exists or is being changed; read it again before retrying"}
	denied      = rule{provider.ClassPermission, Subject + " denied the operation; the identity lacks the required permission"}
	expired     = rule{provider.ClassAuth, "the " + Subject + " session token has expired"}
	invalid     = rule{provider.ClassProviderError, Subject + " rejected the request as invalid; check the arguments"}
	quota       = rule{provider.ClassProviderError, Subject + " refused the operation because a limit or quota is exceeded"}
	// unmapped is for a code the protocol that delivered it does not define.
	unmapped = rule{provider.ClassInvalidResponse, Subject + " returned an error code this protocol does not define"}

	// codeRules is the table error code -> class, keyed by the code on the wire (the awsQueryError code of
	// the model, not the shape name). It is data: add a row, not a branch.
	//
	// The throttling rows are the codes of botocore 1.43.123 (_retry.json) plus RequestLimitExceeded, which the
	// EC2 query API sends. SlowDown is S3's code and not in that source, so the awsQuery services do not
	// define it: it is not trusted as a rate limit here. The S3 package maps it where the model proves it.
	// There is no conflict class: a conflict is a provider-error with a message that says so, and the code
	// stays in Original.
	codeRules = map[string]rule{
		"Throttling":                             rateLimited,
		"ThrottlingException":                    rateLimited,
		"ThrottledException":                     rateLimited,
		"RequestThrottledException":              rateLimited,
		"ProvisionedThroughputExceededException": rateLimited,
		"RequestLimitExceeded":                   rateLimited,
		"TooManyRequestsException":               rateLimited,
		"SlowDown":                               unmapped,

		"AccessDenied":                denied,
		"AccessDeniedException":       denied,
		"InvalidClientTokenId":        {provider.ClassAuth, Subject + " does not recognise the access key"},
		"UnrecognizedClientException": {provider.ClassAuth, Subject + " does not recognise the access key"},
		"SignatureDoesNotMatch":       {provider.ClassAuth, Subject + " rejected the request signature; check the secret access key"},
		"ExpiredToken":                expired,
		"ExpiredTokenException":       expired,

		// IAM: NoSuchEntity is the code of NoSuchEntityException; the report codes and the organization
		// error name an entity that is not there.
		"NoSuchEntity":                  notFound,
		"ReportNotPresent":              notFound,
		"OrganizationNotFoundException": notFound,

		// The restJson1 codes of the Account and SES v2 models. A service package adds the rows of its own
		// errors here; until it does, ClassifyStrict reports such a client error as an unknown code.
		"ResourceNotFoundException":     notFound,
		"ValidationException":           invalid,
		"ConflictException":             conflict,
		"LimitExceededException":        quota,
		"ServiceQuotaExceededException": quota,

		"EntityAlreadyExists":           conflict,
		"DeleteConflict":                conflict,
		"EntityTemporarilyUnmodifiable": conflict,
		"ConcurrentModification":        conflict,
		"NameConflict":                  conflict,
		"DuplicateCertificate":          conflict,
		"DuplicateSSHPublicKey":         conflict,
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

// ClassifyStrict is Classify for the protocols whose models define every error code of an operation, the
// REST ones. A client error with a code that is not in the table is not guessed from its status: it is an
// invalid-provider-response, with the code kept in Original. The status still decides where it means
// something by itself: 401, 403, 429, a server-side status, and an answer without any code.
func ClassifyStrict(op string, status int, header http.Header, code, errType string) (*provider.Error, time.Duration) {
	if _, known := codeRules[code]; code != "" && !known &&
		status >= 400 && status < 500 && status != http.StatusUnauthorized && status != http.StatusForbidden && status != http.StatusTooManyRequests {
		failure := &provider.Error{Class: unmapped.class, Op: op, Message: unmapped.message}
		failure.Original = original(status, code, errType)
		return failure, 0
	}
	return Classify(op, status, header, code, errType)
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

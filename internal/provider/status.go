package provider

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// StatusTexts are the provider-specific parts of the standard status messages. Subject names the provider
// in the messages for 429, 503, 504, a redirect, and any other status. Auth, Permission, and NotFound are the
// complete messages for 401, 403, and 404, because their wording differs from provider to provider.
type StatusTexts struct {
	Subject    string
	Auth       string
	Permission string
	NotFound   string
}

// ClassifyStatus maps an HTTP status of a failed request to a stable class and message. The provider body is
// never read into the message. It covers 401, 403, 404, 429, 503, 504, every 3xx, and any other status.
// Holding a rate limiter for a 429 is up to the caller, which has the response headers; see RetryAfter.
func ClassifyStatus(op string, status int, texts StatusTexts) *Error {
	subject := texts.Subject
	switch {
	case status == http.StatusUnauthorized:
		return &Error{Class: ClassAuth, Op: op, Message: texts.Auth}
	case status == http.StatusForbidden:
		return &Error{Class: ClassPermission, Op: op, Message: texts.Permission}
	case status == http.StatusNotFound:
		return &Error{Class: ClassNotFound, Op: op, Message: texts.NotFound}
	case status == http.StatusTooManyRequests:
		return &Error{Class: ClassRateLimited, Op: op, Message: subject + " rate-limited the operation"}
	case status == http.StatusServiceUnavailable:
		return &Error{Class: ClassUnreachable, Op: op, Message: subject + " is unavailable or in maintenance"}
	case status == http.StatusGatewayTimeout:
		return &Error{Class: ClassTimeout, Op: op, Message: subject + " did not answer in time"}
	case status >= 300 && status < 400:
		return &Error{Class: ClassProviderError, Op: op,
			Message: subject + " answered with a redirect, which Qatlas does not follow for this request"}
	}
	return &Error{Class: ClassProviderError, Op: op,
		Message: subject + " rejected the operation (HTTP " + strconv.Itoa(status) + ")"}
}

// ReadJSON reads a response body of at most limit bytes and decodes it into out. A body that cannot be read
// or exceeds the limit, and a body that is not valid JSON for out, are invalid-response errors. Subject names
// the provider in the message, for example "Baserow". It returns nil on success; callers must test the
// result with != nil before returning it as an error interface.
func ReadJSON(op, subject string, r io.Reader, limit int64, out any) *Error {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || int64(len(data)) > limit {
		return InvalidResponse(op, "the "+subject+" response could not be read within the size limit")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return InvalidResponse(op, subject+" returned an invalid response")
	}
	return nil
}

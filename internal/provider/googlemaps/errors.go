package googlemaps

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

// itoa keeps messages free of fmt for a single number.
func itoa(n int) string { return strconv.Itoa(n) }

// failure is a provider error of the generic class.
func failure(op, message string) *provider.Error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

// badRequest is the refusal of an argument. It names the argument and the rule, never the value.
func badRequest(message string) error {
	return &provider.InvalidRequestError{Message: message}
}

// googleFields maps the Google error object, {"error":{"code","message","status","details":[...]}}, to an
// Original: the status and message of the error and the type and reason of each detail. The domain and the
// metadata of a detail may echo request values and are dropped.
var googleFields = provider.OriginalFields{
	Code: "error.status", Message: "error.message", List: "error.details",
	Item: &provider.OriginalFields{Type: "@type", Code: "reason"},
}

// errorInfoSuffix ends the @type of the detail that carries the machine-readable reason.
const errorInfoSuffix = "google.rpc.ErrorInfo"

// googleStatusOf reads the status string and the reason of the first ErrorInfo detail of a Google error
// object. Anything that is not that object yields empty strings.
func googleStatusOf(body []byte) (status, reason string) {
	var answer struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				Type   string `json:"@type"`
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &answer) != nil {
		return "", ""
	}
	for _, detail := range answer.Error.Details {
		if strings.HasSuffix(detail.Type, errorInfoSuffix) && detail.Reason != "" {
			return answer.Error.Status, detail.Reason
		}
	}
	return answer.Error.Status, ""
}

// googleError maps a failed Google answer to a provider error. Only values Google documents decide beyond the
// HTTP status: 401 with CREDENTIALS_MISSING or UNAUTHENTICATED is auth, 403 with PERMISSION_DENIED is
// permission, and 429 with RATE_LIMIT_EXCEEDED or RESOURCE_EXHAUSTED is rate-limited. Any other value, such
// as API_KEY_INVALID or a quota reason, is not trusted to choose a class: the HTTP status decides, and a 400
// is invalid-provider-response because Qatlas checked the arguments before sending. The message and details
// of the body never reach the message; they go along as the Original.
func googleError(op string, status int, body []byte) *provider.Error {
	failed := func(class provider.Class, message string) *provider.Error {
		return &provider.Error{Class: class, Op: op, Message: message,
			Original: provider.ErrorOriginal(status, body, googleFields)}
	}
	state, reason := googleStatusOf(body)
	switch {
	case status == http.StatusUnauthorized && (reason == "CREDENTIALS_MISSING" || state == "UNAUTHENTICATED"):
		return failed(provider.ClassAuth, "Google reported the credential as missing or not accepted; check the API key")
	case status == http.StatusForbidden && state == "PERMISSION_DENIED":
		return failed(provider.ClassPermission, "Google refused the key for the Places API (New); check that the "+
			"API is enabled in the key's project, that the project has a billing account, and the key's restrictions")
	case status == http.StatusTooManyRequests && (reason == "RATE_LIMIT_EXCEEDED" || state == "RESOURCE_EXHAUSTED"):
		return failed(provider.ClassRateLimited, "Google rate-limited the key or its quota is used up")
	case status == http.StatusBadRequest:
		return failed(provider.ClassInvalidResponse, "Google rejected the request as invalid (HTTP 400) although "+
			"Qatlas Connect checked it; the API may have changed")
	}
	mapped := provider.ClassifyStatus(op, status, provider.StatusTexts{
		Subject:    "Google",
		Auth:       "Google rejected the API key; check the key",
		Permission: "Google refused the key for the Places API (New); check that the API is enabled in the key's project, that the project has a billing account, and the key's restrictions",
		NotFound:   "Google did not find the place; the place ID may be wrong or obsolete",
	})
	mapped.Original = provider.ErrorOriginal(status, body, googleFields)
	return mapped
}

// legacyFields maps the legacy web service error form to an Original.
var legacyFields = provider.OriginalFields{Code: "status", Message: "error_message"}

// legacyStatus reads the legacy web service form, an HTTP 200 answer whose body carries "status". It returns
// that status and a nil error for OK and ZERO_RESULTS, which the caller handles as a result or an empty one.
// OVER_QUERY_LIMIT is rate-limited, REQUEST_DENIED is auth (a key Google does not accept for the API, which
// Google does not tell apart from a missing grant), INVALID_REQUEST is invalid-provider-response, and
// UNKNOWN_ERROR is a provider error the caller does not repeat. A missing or unknown status is an invalid
// response. error_message is provider text: it is kept as the Original and never put in the message.
func legacyStatus(op string, body []byte) (string, *provider.Error) {
	var answer struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(body, &answer) != nil || answer.Status == "" {
		return "", provider.InvalidResponse(op, "Google returned an invalid response")
	}
	failed := func(class provider.Class, message string) (string, *provider.Error) {
		return answer.Status, &provider.Error{Class: class, Op: op, Message: message,
			Original: provider.ErrorOriginal(http.StatusOK, body, legacyFields)}
	}
	switch answer.Status {
	case "OK", "ZERO_RESULTS":
		return answer.Status, nil
	case "OVER_QUERY_LIMIT":
		return failed(provider.ClassRateLimited, "Google rate-limited the key or its quota is used up")
	case "REQUEST_DENIED":
		return failed(provider.ClassAuth, "Google denied the request; the key is not accepted for this API, "+
			"or the API is not enabled or has no billing account")
	case "INVALID_REQUEST":
		return failed(provider.ClassInvalidResponse, "Google rejected the request as invalid although "+
			"Qatlas Connect checked it; the API may have changed")
	case "UNKNOWN_ERROR":
		return failed(provider.ClassProviderError, "Google reported a server error; Qatlas Connect does not repeat the request")
	}
	return failed(provider.ClassInvalidResponse, "Google returned a status Qatlas Connect does not know")
}

package googlepagespeed

import (
	"net/http"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/googleapi"
)

// failure is a provider error of the generic class.
func failure(op, message string) *provider.Error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

// badRequest is the refusal of an argument. It names the argument and the rule, never the value.
func badRequest(message string) error {
	return &provider.InvalidRequestError{Message: message}
}

const (
	notFoundCrux = "the Chrome UX Report has no data for this url or origin; it is not in the dataset, " +
		"which is a result and not a configuration fault"
	notFoundPSI = "Google did not find the analysis target"
	enableHint  = "check that the PageSpeed Insights API and the Chrome UX Report API are enabled in the key's " +
		"project and the key's restrictions"
)

// googleError maps a failed Google answer to a provider error. The HTTP status decides, with one value of the
// Google error object on top: 400 with the reason API_KEY_INVALID is auth, because that is how both services
// answer a key they do not know (observed, not documented). Any other 400 is a provider error: Qatlas checked
// the arguments, so the page that PageSpeed Insights was asked to analyse may be one Google cannot load.
// The message and details of the body never reach the message; they go along as the Original.
func googleError(op string, status int, body []byte, notFound string) *provider.Error {
	_, reason := googleapi.StatusOf(body)
	var mapped *provider.Error
	switch {
	case status == http.StatusBadRequest && reason == "API_KEY_INVALID":
		mapped = &provider.Error{Class: provider.ClassAuth, Op: op, Message: "Google does not accept the API key; check the key"}
	case status == http.StatusBadRequest:
		mapped = failure(op, "Google rejected the request (HTTP 400) although Qatlas Connect checked its form; "+
			"for a PageSpeed Insights run the page may not be loadable by Google")
	default:
		mapped = provider.ClassifyStatus(op, status, provider.StatusTexts{
			Subject:    "Google",
			Auth:       "Google rejected the API key; check the key",
			Permission: "Google refused the key; " + enableHint,
			NotFound:   notFound,
		})
	}
	mapped.Original = provider.ErrorOriginal(status, body, googleapi.Fields)
	return mapped
}

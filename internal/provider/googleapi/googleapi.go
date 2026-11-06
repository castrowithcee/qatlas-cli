// Package googleapi holds the error object form that Google APIs share, so that provider packages read it
// the same way without importing each other.
package googleapi

import (
	"encoding/json"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

// Fields maps the Google error object, {"error":{"code","message","status","details":[...]}}, to an
// Original: the status and message of the error and the type and reason of each detail. The domain and the
// metadata of a detail may echo request values and are dropped.
var Fields = provider.OriginalFields{
	Code: "error.status", Message: "error.message", List: "error.details",
	Item: &provider.OriginalFields{Type: "@type", Code: "reason"},
}

// errorInfoSuffix ends the @type of the detail that carries the machine-readable reason.
const errorInfoSuffix = "google.rpc.ErrorInfo"

// StatusOf reads the status string and the reason of the first ErrorInfo detail of a Google error
// object. Anything that is not that object yields empty strings.
func StatusOf(body []byte) (status, reason string) {
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

package nextcloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The OCS API answers below fixed path segments of the instance. ocsRoot is the version 2 endpoint, which
// mirrors the OCS status in the HTTP status; an app adds its own fixed segments after it.
var (
	ocsRoot    = []string{"ocs", "v2.php"}
	ocsSharing = []string{"apps", "files_sharing", "api", "v1"}
	ocsCloud   = []string{"cloud"}
	ocsSearch  = []string{"search"}

	ocsNotifications = []string{"apps", "notifications", "api", "v2"}
	ocsActivity      = []string{"apps", "activity", "api", "v2"}
	ocsUserStatus    = []string{"apps", "user_status", "api", "v1"}
)

// A successful version 2 answer repeats its 2xx HTTP status; Talk answers a created message with 201.
func ocsSucceeded(code int64) bool { return code >= 200 && code < 300 }

// ocsRequest is one OCS read. The app and the suffix are fixed segments chosen by the calling operation
// (a validated ID is the only part that may come from a request) and the query holds typed values; nothing
// is taken over as a free path, URL, method, or header.
type ocsRequest struct {
	app    []string
	suffix []string
	query  url.Values
	// emptyOnNotModified reads a 304 answer as an empty result instead of a redirect; the Talk chat API
	// answers a page without messages that way.
	emptyOnNotModified bool
	// ifNoneMatch is a validated opaque ETag sent as a precondition; the caller sets it only with
	// emptyOnNotModified and reads notModified to tell a 304 from an answer without data.
	ifNoneMatch string
	notModified *bool
	// emptyOnNoContent reads a 204 answer as an empty result; the notifications and activity lists answer
	// that way when there is nothing to report.
	emptyOnNoContent bool
}

// ocsEnvelope is the answer shape every OCS endpoint shares.
type ocsEnvelope struct {
	OCS *struct {
		Meta *struct {
			Status     string      `json:"status"`
			StatusCode json.Number `json:"statuscode"`
		} `json:"meta"`
		Data json.RawMessage `json:"data"`
	} `json:"ocs"`
}

// ocsGet performs one authenticated OCS GET and returns the data member of a successful envelope. The
// message of the envelope and the body of a failed answer are never read into an error.
func (c *Client) ocsGet(ctx context.Context, op string, request ocsRequest) (json.RawMessage, error) {
	data, _, err := c.ocsDo(ctx, op, http.MethodGet, request, nil, "")
	return data, err
}

// ocsGetHeader is ocsGet that also returns the response headers. The data of an accepted 304 is nil.
func (c *Client) ocsGetHeader(ctx context.Context, op string, request ocsRequest) (json.RawMessage, http.Header, error) {
	return c.ocsDo(ctx, op, http.MethodGet, request, nil, "")
}

// ocsSend performs one authenticated OCS change with a form-encoded body of typed values. The outcome of a
// request that was sent and not clearly refused is open, so such an error carries the uncertain hint.
func (c *Client) ocsSend(ctx context.Context, op, method string, request ocsRequest, form url.Values, uncertain string) (json.RawMessage, error) {
	data, _, err := c.ocsDo(ctx, op, method, request, form, uncertain)
	return data, err
}

func (c *Client) ocsDo(ctx context.Context, op, method string, request ocsRequest, form url.Values,
	uncertain string) (json.RawMessage, http.Header, error) {
	unclear := func(err error) error {
		if uncertain == "" {
			return err
		}
		return withUncertainty(err, uncertain)
	}
	segments := append(append(append(append([]string{}, c.install...), ocsRoot...), request.app...), request.suffix...)
	query := url.Values{"format": {"json"}}
	for name, values := range request.query {
		query[name] = values
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+escapePath(segments)+"?"+query.Encode(), body)
	if err != nil {
		return nil, nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")
	if request.ifNoneMatch != "" {
		req.Header.Set("If-None-Match", request.ifNoneMatch)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	response, err := c.http.Do(req)
	if err != nil {
		if uncertain != "" {
			return nil, nil, sentTransportError(op, err, uncertain)
		}
		return nil, nil, provider.Transport(op, "Nextcloud", err)
	}
	defer response.Body.Close()
	if request.emptyOnNoContent && response.StatusCode == http.StatusNoContent {
		return nil, response.Header, nil
	}
	if request.emptyOnNotModified && response.StatusCode == http.StatusNotModified {
		if request.notModified != nil {
			*request.notModified = true
		}
		return nil, response.Header, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if uncertain != "" {
			return nil, nil, sentFormatStatusError(op, response, uncertain, ocsFormat)
		}
		return nil, nil, formatStatusError(op, response, ocsFormat)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil || len(raw) > maxBodyBytes {
		return nil, nil, unclear(invalidResponse(op, "the Nextcloud response could not be read within the size limit"))
	}
	var envelope ocsEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.OCS == nil || envelope.OCS.Meta == nil {
		return nil, nil, unclear(invalidResponse(op, "Nextcloud did not answer with an OCS document"))
	}
	meta := envelope.OCS.Meta
	code, err := meta.StatusCode.Int64()
	if err != nil {
		return nil, nil, unclear(invalidResponse(op, "Nextcloud did not answer with an OCS document"))
	}
	if !ocsSucceeded(code) || !strings.EqualFold(meta.Status, "ok") {
		// A version 2 answer repeats its failure in the HTTP status; a body that disagrees with a
		// successful status is classified by its own code and otherwise refused as unusable.
		// The envelope is the original of such a refusal; only its meta is read, never its data.
		if code >= 400 && code < 600 {
			failure := statusError(op, int(code))
			if uncertain != "" {
				failure = sentStatusError(op, int(code), uncertain)
			}
			return nil, nil, withOriginalValue(failure, provider.ErrorOriginal(response.StatusCode,
				raw[:min(len(raw), provider.ErrorBodyLimit)], ocsOriginalFields))
		}
		return nil, nil, unclear(invalidResponse(op, "Nextcloud answered with an unexpected OCS status"))
	}
	return envelope.OCS.Data, response.Header, nil
}

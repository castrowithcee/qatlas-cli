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
)

// ocsOK is the OCS status code of a successful version 2 answer.
const ocsOK = 200

// ocsRequest is one OCS read. The app and the suffix are fixed segments chosen by the calling operation
// (a validated ID is the only part that may come from a request) and the query holds typed values; nothing
// is taken over as a free path, URL, method, or header.
type ocsRequest struct {
	app    []string
	suffix []string
	query  url.Values
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
	segments := append(append(append(append([]string{}, c.install...), ocsRoot...), request.app...), request.suffix...)
	query := url.Values{"format": {"json"}}
	for name, values := range request.query {
		query[name] = values
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.origin+escapePath(segments)+"?"+query.Encode(), nil)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")

	response, err := c.http.Do(req)
	if err != nil {
		return nil, provider.Transport(op, "Nextcloud", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, statusError(op, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		return nil, invalidResponse(op, "the Nextcloud response could not be read within the size limit")
	}
	var envelope ocsEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.OCS == nil || envelope.OCS.Meta == nil {
		return nil, invalidResponse(op, "Nextcloud did not answer with an OCS document")
	}
	meta := envelope.OCS.Meta
	code, err := meta.StatusCode.Int64()
	if err != nil {
		return nil, invalidResponse(op, "Nextcloud did not answer with an OCS document")
	}
	if code != ocsOK || !strings.EqualFold(meta.Status, "ok") {
		// A version 2 answer repeats its failure in the HTTP status; a body that disagrees with a
		// successful status is classified by its own code and otherwise refused as unusable.
		if code >= 400 && code < 600 {
			return nil, statusError(op, int(code))
		}
		return nil, invalidResponse(op, "Nextcloud answered with an unexpected OCS status")
	}
	return envelope.OCS.Data, nil
}

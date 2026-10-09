package infomaniakdav

import (
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
)

// Caps on what one request may read, shared with the parsers.
const (
	maxResponseBytes = dav.MaxResponseBytes
	maxEventBytes    = dav.MaxEventBytes
)

// server binds the shared DAV parsers to this provider's name and its one origin.
var server = dav.Server{Name: "Infomaniak", Origin: origin}

// failure reports why a resource carries no usable data.
func failure(r *dav.Resource, op string) error {
	if code, ok := dav.StatusCodeOf(r.Status); ok && (code < 200 || code >= 300) {
		return statusError(op, code)
	}
	if !r.Read {
		return invalidResponse(op, "Infomaniak answered without readable properties for this node")
	}
	return nil
}

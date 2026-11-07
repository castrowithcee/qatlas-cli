package restjson

import "github.com/castrowithcee/qatlas-connect/internal/provider/aws/httpbind"

// NewClient returns the REST client with the restJson1 body format. The caller fills HTTP, Signer and URL
// (the endpoint of the table); the host never comes from a request.
func NewClient(base httpbind.Client) *httpbind.Client {
	base.Codec = Codec{}
	return &base
}

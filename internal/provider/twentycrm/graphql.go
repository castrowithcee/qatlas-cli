package twentycrm

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The two GraphQL endpoints of a Twenty server. Requests go only to these paths of the configured origin.
const (
	graphqlPath  = "/graphql"
	metadataPath = "/metadata"
)

// graphqlDocument is a fixed GraphQL document bound to its endpoint. The type is unexported and its text is
// always a package constant: no argument of an agent becomes part of a document, only a variable value.
type graphqlDocument struct {
	path string
	text string
}

type graphqlEnvelope struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Extensions struct {
			Code string `json:"code"`
		} `json:"extensions"`
	} `json:"errors"`
}

// graphql sends one fixed document with checked variables and decodes its data into out. Twenty answers a
// failed query with HTTP 200 and an errors member, which is never a success. The read is sent once like every
// other read helper; the provider text of an error is not copied, only its code selects the class.
func (c *Client) graphql(ctx context.Context, op string, doc graphqlDocument, variables map[string]any,
	out any) error {
	return c.graphqlWith(ctx, op, "", doc, variables, out)
}

// graphqlWith is graphql for a mutation: uncertain is appended to every failure after which the change may have
// taken effect (transport, 5xx, unreadable answer, errors other than a clear authentication or permission one).
func (c *Client) graphqlWith(ctx context.Context, op, uncertain string, doc graphqlDocument,
	variables map[string]any, out any) error {
	payload := map[string]any{"query": doc.text, "variables": variables}
	var envelope graphqlEnvelope
	if err := c.changeWith(ctx, op, uncertain, http.MethodPost, doc.path, payload, &envelope); err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		for _, e := range envelope.Errors {
			switch e.Extensions.Code {
			case "UNAUTHENTICATED":
				return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "Twenty rejected the API key"}
			case "FORBIDDEN":
				return &provider.Error{Class: provider.ClassPermission, Op: op, Message: "the workspace role of " +
					"this API key may not perform this operation; check the role of the API key in Twenty"}
			}
		}
		return providerError(op, "Twenty rejected the query"+uncertain)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" || json.Unmarshal(envelope.Data, out) != nil {
		return provider.InvalidResponse(op, "Twenty returned an invalid response"+uncertain)
	}
	return nil
}

// Package googlemaps implements read-only access to Google Maps Platform with one API key. It starts with
// Places API (New): the details of one place.
//
// A connection binds one API key, sent only in the X-Goog-Api-Key header, and takes no target: the key
// reaches what its Cloud project and its restrictions allow. The base URL is fixed to the official origin;
// any other value is refused before the key is read.
//
// Google bills a Places request by the fields it asks for, so a field mask is mandatory: it is checked
// against a table of known field paths, with no default and no wildcard, before the key is read. A request
// is sent once and never repeated, and the requests of one key are spaced.
//
// Everything Google returns is untrusted third-party content: strings and lists are bounded, and addresses
// are text that is never fetched. The error helpers of this package serve the two error forms of the Maps
// web services, the Google error object and the legacy status in an HTTP 200 body.
package googlemaps

import (
	"context"
	"errors"
	"net/url"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "google.maps"

// roleKey is the single secret role: the API key of a Google Cloud project.
const roleKey = "api_key"

// probePlaceID is the place of the connection test: the place ID of the Place Details examples in Google's
// documentation. A place ID can become obsolete; the test then ends as not-found.
const probePlaceID = "ChIJj61dQgK6j4AR4GeTYWZsKWw"

// TestConnection reads one place with the field mask id, the IDs Only group, which Google documents as
// unlimited and free. A success proves that the key reaches Places API (New) and that its project may call it;
// it says nothing about any other Maps API.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	const op = "test connection"
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return testClass(err)
	}
	if _, err := client.get(ctx, op, "/v1/places/"+url.PathEscape(probePlaceID), nil, "id"); err != nil {
		return testClass(err)
	}
	return provider.ClassOK, nil
}

// testClass turns a provider failure into the class of a connection test; any other error is returned.
func testClass(err error) (provider.Class, error) {
	var failed *provider.Error
	if errors.As(err, &failed) {
		return failed.Class, nil
	}
	return "", err
}

// noTarget refuses every configured target: a key reaches what its project allows, not a part of an account.
func noTarget(string) error {
	return errors.New("a Google Maps connection takes no target; leave the target empty")
}

// Register adds the provider metadata, its connection test, and its tools.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Google Maps", DefaultBaseURL: apiRoot,
		Description:    "Places data from Google Maps Platform with an API key, billed per field group by Google",
		DefaultEffects: []config.Effect{config.EffectRead},
		ValidateBaseURL: func(raw string) error {
			if !isAPIRoot(raw) {
				return errors.New("a Google Maps service must use the official API origin " + apiRoot)
			}
			return nil
		},
		SecretRoles: []config.SecretRole{{
			Name: roleKey,
			Description: "API key of a Google Cloud project with Places API (New) enabled and a billing account; " +
				"requests are billed to that project",
		}},
		Target: config.TargetMetadata{
			Label:       "target",
			Description: "none: a Google Maps key reaches what its project allows, so leave the target empty",
			Validate:    noTarget,
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read", Recommended: true,
			Description: "reads place details from Google Places; the field mask of each request decides what " +
				"Google bills, and nothing is changed",
			Tools: []string{placesGet.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider, capability.Operation{Descriptor: placesGet, Handler: handlePlacesGet})
}

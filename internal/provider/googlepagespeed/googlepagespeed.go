// Package googlepagespeed implements read-only access to Google's page analysis services with one API key:
// PageSpeed Insights (Lighthouse lab data plus Chrome field data of one page) and the Chrome UX Report API
// (field data of one page or origin, current and as a history).
//
// A connection binds one API key and takes no target: the key reaches what its Cloud project allows. Both
// service origins are fixed in this package; a configured base URL other than the default is refused before
// the key is read. The two services take the key only as the query parameter key, so the full request URL
// is a secret: it never reaches a message, a log, an error, or a result, and the key is registered with the
// redactor as soon as it is resolved.
//
// The url and origin arguments name pages that Google analyses or looks up. Qatlas never fetches them; it
// checks their form and hands them to Google. Everything Google returns is untrusted third-party content:
// strings, lists, and maps are bounded, links are text that is never fetched, and the large parts of a
// Lighthouse result (screenshots, the detail tables of the audits) are left out.
package googlepagespeed

import (
	"context"
	"errors"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "google.pagespeed"

// roleKey is the single secret role: the API key of a Google Cloud project.
const roleKey = "api_key"

// probeOrigin is the origin of the connection test: web.dev, an origin of Google's own that its PageSpeed
// Insights documentation uses as an example. The test does not depend on it having data: NOT_FOUND counts
// as a success, because Google checks the key and the API before it looks the origin up.
const probeOrigin = "https://web.dev"

// TestConnection asks the Chrome UX Report API for one metric of one fixed origin. A 200 answer or NOT_FOUND
// proves that the key is valid and that the Chrome UX Report API is enabled for its project. It does not
// prove the PageSpeed Insights API or its daily quota.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return testClass(err)
	}
	var out queryResponse
	err = client.send(ctx, apiCall{op: "test connection", method: "POST", root: cruxRoot, path: cruxRecordPath,
		body:  queryRequest{Origin: probeOrigin, Metrics: []string{"first_contentful_paint"}},
		limit: maxCruxBytes, notFound: notFoundCrux}, &out)
	if err == nil {
		return provider.ClassOK, nil
	}
	var failed *provider.Error
	if errors.As(err, &failed) && failed.Class == provider.ClassNotFound {
		return provider.ClassOK, nil
	}
	return testClass(err)
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
	return errors.New("a Google PageSpeed connection takes no target; leave the target empty")
}

// Register adds the provider metadata, its connection test, and its tools.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Google PageSpeed", DefaultBaseURL: psiRoot,
		Description:    "Page speed and real-user field data from PageSpeed Insights and the Chrome UX Report with an API key",
		DefaultEffects: []config.Effect{config.EffectRead},
		ValidateBaseURL: func(raw string) error {
			if !isDefaultRoot(raw) {
				return errors.New("a Google PageSpeed service has fixed origins; leave the base URL as " + psiRoot)
			}
			return nil
		},
		SecretRoles: []config.SecretRole{{
			Name: roleKey,
			Description: "API key of a Google Cloud project with the PageSpeed Insights API enabled and, for field " +
				"data, the Chrome UX Report API",
		}},
		Target: config.TargetMetadata{
			Label:       "target",
			Description: "none: a Google key reaches what its project allows, so leave the target empty",
			Validate:    noTarget,
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read", Recommended: true,
			Description: "analyses a page with PageSpeed Insights and reads Chrome UX Report field data; nothing is changed",
			Tools:       []string{pagespeedRun.ID, cruxQueryRecord.ID, cruxQueryHistory.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	for _, op := range []capability.Operation{
		{Descriptor: pagespeedRun, Handler: handlePagespeedRun},
		{Descriptor: cruxQueryRecord, Handler: handleQueryRecord},
		{Descriptor: cruxQueryHistory, Handler: handleQueryHistory},
	} {
		if err := reg.Register(Provider, op); err != nil {
			return err
		}
	}
	return nil
}

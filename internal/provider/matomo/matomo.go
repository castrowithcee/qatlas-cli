// Package matomo implements controlled access to a Matomo instance, on-premise or Matomo Cloud, through
// its HTTP API with a personal authentication token.
//
// Every request is a POST to <base>/index.php with a form body naming module=API, a fixed method of the
// calling tool, and format=JSON. The token travels in an Authorization bearer header, never in a URL or an
// argument; only an instance older than 5.4.0, which reads no header, gets it as the token_auth form field.
// Matomo answers application errors with HTTP 200 and {"result":"error"}; client.go and errors.go map them
// to the stable classes without ever copying Matomo's message text.
//
// The base URL is required and has no default. A connection reaches websites (site, site/ID) and, only when
// bound explicitly, the instance itself. Every Matomo value is untrusted provider data: strings and lists
// are bounded, a response is read up to a size limit, and addresses are text that is never fetched.
package matomo

import (
	"context"
	"errors"
	"slices"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "matomo"

// roleToken is the single secret role: a personal Matomo authentication token.
const roleToken = "token"

// TestConnection reads the instance version, which proves the token and enforces Matomo 5, and then proves
// each website the connection names. It costs nothing and proves neither write rights nor further methods.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	const op = "test connection"
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return testClass(err)
	}
	info, err := client.instance(ctx, op, true)
	if err != nil {
		return testClass(err)
	}
	if err := client.supports(info, baseRequirement("")); err != nil {
		return testClass(err)
	}
	for _, id := range client.scope.sites {
		if _, err := client.fetchSite(ctx, op, baseRequirement(""), id); err != nil {
			return testClass(err)
		}
	}
	return provider.ClassOK, nil
}

// testClass turns a failure into the class a connection test reports. A version or method the instance lacks
// fails closed as a provider error.
func testClass(err error) (provider.Class, error) {
	var failure *provider.Error
	if errors.As(err, &failure) {
		return failure.Class, nil
	}
	var unavailable *unavailableError
	var invalid *provider.InvalidRequestError
	if errors.As(err, &unavailable) || errors.As(err, &invalid) {
		return provider.ClassProviderError, nil
	}
	return "", err
}

// Register adds the provider metadata, its connection test, and its tools.
func Register(reg *capability.Registry) error {
	readTools := []string{versionGet.ID, sitesList.ID, sitesGet.ID, reportsList.ID, reportsDescribe.ID,
		segmentsFields.ID, segmentsSuggest.ID, reportsGet.ID, goalsList.ID, goalsGet.ID, segmentsList.ID,
		segmentsGet.ID, annotationsList.ID, annotationsGet.ID, annotationsCount.ID, trackingCodeGet.ID,
		siteSettingsGet.ID, consentManagerDetect.ID, dimensionsList.ID, referenceDataList.ID, ipRangesExpand.ID}
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Matomo",
		Description:    "Web analytics, on-premise or Matomo Cloud, through a personal authentication token",
		DefaultEffects: []config.Effect{config.EffectRead},
		ValidateBaseURL: func(raw string) error {
			_, err := originOf(raw)
			return err
		},
		SecretRoles: []config.SecretRole{{
			Name: roleToken,
			Description: "Personal Matomo authentication token, created under Personal, Security; it has the " +
				"rights of its user per website and is sent as 'Authorization: Bearer ...'",
		}},
		Target: config.TargetMetadata{
			Label:    "website",
			Multiple: true,
			Description: "websites the token reaches, as all of them or as single websites by ID; the instance " +
				"itself only when bound explicitly. Without targets a connection reaches every website of the " +
				"token and no instance-wide tool",
			Kinds: []config.TargetKind{{
				Name: "site",
				Description: "site binds every website of the token and cannot be combined with site/ID; " +
					"site/ID binds exactly that website",
				Forms: []string{formSites, formSite},
			}, {
				Name:        "instance",
				Description: "the Matomo instance itself, for tools that need Super User rights",
				Forms:       []string{formInstance},
				Unlisted:    true,
			}},
			Validate:    validateTarget,
			ValidateSet: validateSet,
			Reaches:     reaches,
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read websites", Recommended: true,
			Description: "reads the instance version, the websites of the token, their tracking code, settings, and " +
				"consent manager, the goals, the saved segments, the annotations, the custom dimensions, the report and " +
				"segment catalog of a website, the data of its reports, reference data, and IP ranges; changes nothing",
			Tools: readTools,
		}, {
			// Live data is personal data (visitor IDs, IP addresses, user IDs, locations), so it is a
			// separate choice and not part of the recommended start.
			ID: "live", Title: "Read websites and live visits",
			Description: "reads what the read profile reads, the visit log with visitor IDs, IP addresses, user IDs, " +
				"and locations, the live counters, the time of the latest visit, and visitor profiles; changes nothing",
			Tools: append(slices.Clone(readTools), visitsList.ID, liveCountersGet.ID, visitsGetLatestTime.ID,
				visitorsGetProfile.ID, visitorsGetLatestID.ID, visitsGetFirst.ID),
		}, {
			ID: "goals", Title: "Manage goals",
			Description: "reads what the read profile reads and adds and updates the goals of a website, and reads, scans for, and dismisses goal recommendations; every " +
				"change needs its own confirmation, and deleting a goal stays unticked because it also deletes " +
				"the conversions recorded for it",
			Tools: append(slices.Clone(readTools), goalsAdd.ID, goalsUpdate.ID,
				goalRecommendationsList.ID, goalRecommendationsScan.ID, goalRecommendationsDismiss.ID),
		}, {
			ID: "segments", Title: "Manage segments",
			Description: "reads what the read profile reads and adds, updates, and stars the saved segments of the " +
				"instance; every change needs its own confirmation, deleting a segment stays unticked, and the " +
				"right to change a segment is Matomo's, per website and per creator",
			Tools: append(slices.Clone(readTools), segmentsAdd.ID, segmentsUpdate.ID, segmentsStar.ID,
				segmentsUnstar.ID),
		}, {
			ID: "sites", Title: "Manage websites",
			Description: "reads what the read profile reads, changes the settings and alias URLs of a website, and " +
				"sets the creation date of websites; every change needs its own confirmation, replacing the alias " +
				"URLs removes those not in the list, and adding a website (Super User, instance-wide) and deleting " +
				"one stay unticked",
			Tools: append(slices.Clone(readTools), sitesUpdate.ID, siteURLsAdd.ID, siteURLsSet.ID,
				sitesUpdateCreatedTime.ID),
		}, {
			ID: "annotations", Title: "Manage annotations",
			Description: "reads what the read profile reads and adds and updates the annotations of a website; " +
				"every change needs its own confirmation",
			Tools: append(slices.Clone(readTools), annotationsAdd.ID, annotationsUpdate.ID),
		}, {
			ID: "dimensions", Title: "Manage custom dimensions",
			Description: "reads what the read profile reads, the dimension options, which need a write right, and " +
				"adds and updates the custom dimensions of a website; every change needs its own confirmation, and a " +
				"new dimension uses a slot for good because Matomo cannot delete one",
			Tools: append(slices.Clone(readTools), dimensionOptionsList.ID, dimensionsAdd.ID, dimensionsUpdate.ID),
		}},
	}, TestConnection); err != nil {
		return err
	}
	if err := reg.RegisterTargetBrowser(Provider, targetBrowser()); err != nil {
		return err
	}
	if err := reg.Register(Provider, globalSetOperations()...); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: versionGet, Handler: capability.Handler(invokeVersionGet)},
		capability.Operation{Descriptor: sitesList, Handler: capability.Handler(invokeSitesList)},
		capability.Operation{Descriptor: sitesGet, Handler: capability.Handler(invokeSitesGet)},
		capability.Operation{Descriptor: reportsList, Handler: capability.Handler(invokeReportsList)},
		capability.Operation{Descriptor: reportsDescribe, Handler: capability.Handler(invokeReportsDescribe)},
		capability.Operation{Descriptor: reportsGet, Handler: capability.Handler(invokeReportsGet)},
		capability.Operation{Descriptor: segmentsFields, Handler: capability.Handler(invokeSegmentsFields)},
		capability.Operation{Descriptor: segmentsSuggest, Handler: capability.Handler(invokeSegmentsSuggest)},
		capability.Operation{Descriptor: visitsList, Handler: capability.Handler(invokeVisitsList)},
		capability.Operation{Descriptor: liveCountersGet, Handler: capability.Handler(invokeLiveCountersGet)},
		capability.Operation{Descriptor: visitsGetLatestTime, Handler: capability.Handler(invokeVisitsGetLatestTime)},
		capability.Operation{Descriptor: visitorsGetProfile, Handler: capability.Handler(invokeVisitorsGetProfile)},
		capability.Operation{Descriptor: visitorsGetLatestID, Handler: capability.Handler(invokeVisitorsGetLatestID)},
		capability.Operation{Descriptor: visitsGetFirst, Handler: capability.Handler(invokeVisitsGetFirst)},
		capability.Operation{Descriptor: goalsList, Handler: capability.Handler(invokeGoalsList)},
		capability.Operation{Descriptor: goalsGet, Handler: capability.Handler(invokeGoalsGet)},
		capability.Operation{Descriptor: goalsAdd, Handler: capability.Handler(invokeGoalsAdd)},
		capability.Operation{Descriptor: goalsUpdate, Handler: capability.Handler(invokeGoalsUpdate)},
		capability.Operation{Descriptor: goalsDelete, Handler: capability.Handler(invokeGoalsDelete)},
		capability.Operation{Descriptor: segmentsList, Handler: capability.Handler(invokeSegmentsList)},
		capability.Operation{Descriptor: segmentsGet, Handler: capability.Handler(invokeSegmentsGet)},
		capability.Operation{Descriptor: segmentsAdd, Handler: capability.Handler(invokeSegmentsAdd)},
		capability.Operation{Descriptor: segmentsUpdate, Handler: capability.Handler(invokeSegmentsUpdate)},
		capability.Operation{Descriptor: segmentsDelete, Handler: capability.Handler(invokeSegmentsDelete)},
		capability.Operation{Descriptor: segmentsStar, Handler: invokeSegmentsMark(true)},
		capability.Operation{Descriptor: segmentsUnstar, Handler: invokeSegmentsMark(false)},
		capability.Operation{Descriptor: annotationsList, Handler: capability.Handler(invokeAnnotationsList)},
		capability.Operation{Descriptor: annotationsGet, Handler: capability.Handler(invokeAnnotationsGet)},
		capability.Operation{Descriptor: annotationsAdd, Handler: capability.Handler(invokeAnnotationsAdd)},
		capability.Operation{Descriptor: annotationsUpdate, Handler: capability.Handler(invokeAnnotationsUpdate)},
		capability.Operation{Descriptor: annotationsDelete, Handler: capability.Handler(invokeAnnotationsDelete)},
		capability.Operation{Descriptor: annotationsDeleteAll, Handler: capability.Handler(invokeAnnotationsDeleteAll)},
		capability.Operation{Descriptor: annotationsCount, Handler: capability.Handler(invokeAnnotationsCount)},
		capability.Operation{Descriptor: dimensionsList, Handler: capability.Handler(invokeDimensionsList)},
		capability.Operation{Descriptor: dimensionOptionsList, Handler: capability.Handler(invokeDimensionOptionsList)},
		capability.Operation{Descriptor: dimensionsAdd, Handler: capability.Handler(invokeDimensionsAdd)},
		capability.Operation{Descriptor: dimensionsUpdate, Handler: capability.Handler(invokeDimensionsUpdate)},
		capability.Operation{Descriptor: goalRecommendationsList, Handler: capability.Handler(invokeGoalRecommendationsList)},
		capability.Operation{Descriptor: goalRecommendationsScan, Handler: capability.Handler(invokeGoalRecommendationsScan)},
		capability.Operation{Descriptor: goalRecommendationsDismiss, Handler: capability.Handler(invokeGoalRecommendationsDismiss)},
		capability.Operation{Descriptor: siteURLsAdd, Handler: invokeSiteURLs(siteURLsAdd, "SitesManager.addSiteAliasUrls", "add website URLs", false)},
		capability.Operation{Descriptor: siteURLsSet, Handler: invokeSiteURLs(siteURLsSet, "SitesManager.setSiteAliasUrls", "set website URLs", true)},
		capability.Operation{Descriptor: sitesUpdateCreatedTime, Handler: capability.Handler(invokeSitesUpdateCreatedTime)},
		capability.Operation{Descriptor: siteGroupsRename, Handler: capability.Handler(invokeSiteGroupsRename)},
		capability.Operation{Descriptor: trackingCodeGet, Handler: capability.Handler(invokeTrackingCodeGet)},
		capability.Operation{Descriptor: siteSettingsGet, Handler: capability.Handler(invokeSiteSettingsGet)},
		capability.Operation{Descriptor: consentManagerDetect, Handler: capability.Handler(invokeConsentManagerDetect)},
		capability.Operation{Descriptor: sitesAdd, Handler: capability.Handler(invokeSitesAdd)},
		capability.Operation{Descriptor: sitesUpdate, Handler: capability.Handler(invokeSitesUpdate)},
		capability.Operation{Descriptor: sitesDelete, Handler: capability.Handler(invokeSitesDelete)},
		capability.Operation{Descriptor: siteGroupsList, Handler: capability.Handler(invokeSiteGroupsList)},
		capability.Operation{Descriptor: globalSettingsGet, Handler: capability.Handler(invokeGlobalSettingsGet)},
		capability.Operation{Descriptor: referenceDataList, Handler: capability.Handler(invokeReferenceDataList)},
		capability.Operation{Descriptor: ipRangesExpand, Handler: capability.Handler(invokeIPRangesExpand)},
	)
}

package github

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The members of a secret scanning alert beyond its flat members, and the filters of the list. Only the
// members here pass through the projection helpers; the flat members keep their handling. The secret, a
// snippet, and every comment field have no place in the raw shapes below, so decoding discards them. A
// member of an unexpected shape is left out and never fails the alert.

const (
	secretPathLimit  = 1024
	maxSecretValues  = 20
	secretSlugLimit  = 100
	secretHashLength = 64
)

var (
	secretValidities = []string{"active", "inactive", "unknown"}
	secretMetadata   = []string{"owner-email", "owner-id", "owner-name", "secret-id", "secret-name",
		"secret-issued-date", "secret-expiration-date", "organization-name", "organization-id", "last-used-date",
		"has-organization-access"}
	secretAlertLinkKeys    = []string{"url", "locations_url", "push_protection_bypass_request_html_url"}
	secretLocationLinkKeys = []string{"blob_url", "commit_url", "page_url", "issue_title_url", "issue_body_url",
		"issue_comment_url", "discussion_title_url", "discussion_body_url", "discussion_comment_url",
		"pull_request_title_url", "pull_request_body_url", "pull_request_comment_url", "pull_request_review_url",
		"pull_request_review_comment_url"}
	ownerEmailHashReg = regexp.MustCompile(`^[0-9a-f]{64}$`)
	secretSlugReg     = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
)

// ---- Output

// SecretScanningLocation is the first location a secret was detected in. It names where, never what.
type SecretScanningLocation struct {
	Path          string `json:"path,omitempty"`
	PathTruncated bool   `json:"path_truncated,omitempty"`
	StartLine     *int64 `json:"start_line,omitempty"`
	EndLine       *int64 `json:"end_line,omitempty"`
	StartColumn   *int64 `json:"start_column,omitempty"`
	EndColumn     *int64 `json:"end_column,omitempty"`
	BlobSHA       string `json:"blob_sha,omitempty"`
	CommitSHA     string `json:"commit_sha,omitempty"`
	URL           string `json:"url,omitempty"`
	Links         Links  `json:"links,omitempty"`
}

// ---- Raw

// secretLocationRaw names the members read from a location; a snippet, content, or secret is not among them.
type secretLocationRaw struct {
	Path        *string      `json:"path"`
	StartLine   *json.Number `json:"start_line"`
	EndLine     *json.Number `json:"end_line"`
	StartColumn *json.Number `json:"start_column"`
	EndColumn   *json.Number `json:"end_column"`
	BlobSHA     *string      `json:"blob_sha"`
	CommitSHA   *string      `json:"commit_sha"`
	HTMLURL     *string      `json:"html_url"`
	links       Links
}

func (r *secretLocationRaw) UnmarshalJSON(data []byte) (err error) {
	type plain secretLocationRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, secretLocationLinkKeys)
	return err
}

// secretExtrasRaw holds the members of an alert beyond its flat members.
type secretExtrasRaw struct {
	provider, providerSlug, bypassedAt *string
	base64, moreLocations              *bool
	links                              Links
	location                           *secretLocationRaw
	resolvedBy, bypassedBy, reviewer   *accountRaw
	assignedTo, closureReviewer        *accountRaw
}

// readSecretExtras decodes each member on its own from explicitly named keys, so one of an unexpected shape
// only drops itself and nothing unnamed is carried along.
func readSecretExtras(data []byte) secretExtrasRaw {
	var x secretExtrasRaw
	var all map[string]json.RawMessage
	if json.Unmarshal(data, &all) != nil {
		return x
	}
	x.links, _ = readLinks(data, secretAlertLinkKeys)
	read := func(key string, dst any) {
		if json.Unmarshal(all[key], dst) != nil {
			switch p := dst.(type) {
			case **string:
				*p = nil
			case **bool:
				*p = nil
			case **accountRaw:
				*p = nil
			case **secretLocationRaw:
				*p = nil
			}
		}
	}
	read("provider", &x.provider)
	read("provider_slug", &x.providerSlug)
	read("push_protection_bypassed_at", &x.bypassedAt)
	read("is_base64_encoded", &x.base64)
	read("has_more_locations", &x.moreLocations)
	read("first_location_detected", &x.location)
	read("resolved_by", &x.resolvedBy)
	read("push_protection_bypassed_by", &x.bypassedBy)
	read("push_protection_bypass_request_reviewer", &x.reviewer)
	read("assigned_to", &x.assignedTo)
	read("closure_request_reviewer", &x.closureReviewer)
	return x
}

func wholeNumber(n *json.Number) *int64 {
	if n == nil {
		return nil
	}
	v, err := n.Int64()
	if err != nil {
		return nil
	}
	return &v
}

func (x secretExtrasRaw) addTo(v *SecretScanningAlert) {
	v.Provider = shortString(x.provider)
	v.ProviderSlug = shortString(x.providerSlug)
	v.IsBase64Encoded = x.base64
	v.HasMoreLocations = x.moreLocations
	v.PushProtectionBypassedAt = shortString(x.bypassedAt)
	v.PushProtectionBypassedBy = x.bypassedBy.project()
	v.PushProtectionBypassRequestReviewer = x.reviewer.project()
	v.ResolvedByDetail = x.resolvedBy.project()
	v.AssignedTo = x.assignedTo.project()
	v.ClosureRequestReviewer = x.closureReviewer.project()
	v.Links = x.links
	if l := x.location; l != nil {
		loc := SecretScanningLocation{StartLine: wholeNumber(l.StartLine), EndLine: wholeNumber(l.EndLine),
			StartColumn: wholeNumber(l.StartColumn), EndColumn: wholeNumber(l.EndColumn),
			BlobSHA: shortString(l.BlobSHA), CommitSHA: shortString(l.CommitSHA), Links: l.links}
		loc.Path, loc.PathTruncated = textOf(l.Path, secretPathLimit)
		if u := address(l.HTMLURL); u != nil {
			loc.URL = *u
		}
		if loc.Path != "" || loc.StartLine != nil || loc.EndLine != nil || loc.StartColumn != nil ||
			loc.EndColumn != nil || loc.BlobSHA != "" || loc.CommitSHA != "" || loc.URL != "" || loc.Links != nil {
			v.FirstLocationDetected = &loc
		}
	}
}

// ---- Schema

var secretLocationSchema = objectSchema(`"path":{"type":"string"},"path_truncated":{"type":"boolean"},` +
	`"start_line":{"type":"integer"},"end_line":{"type":"integer"},"start_column":{"type":"integer"},` +
	`"end_column":{"type":"integer"},"blob_sha":{"type":"string"},"commit_sha":{"type":"string"},` +
	`"url":{"type":"string"},"links":` + linksSchema(secretLocationLinkKeys))

var secretDetailProperties = `,"provider":{"type":"string"},"provider_slug":{"type":"string"},` +
	`"is_base64_encoded":{"type":"boolean"},"has_more_locations":{"type":"boolean"},` +
	`"push_protection_bypassed_by":` + restAccountSchema + `,"push_protection_bypassed_at":{"type":"string"},` +
	`"push_protection_bypass_request_reviewer":` + restAccountSchema + `,"resolved_by_detail":` +
	restAccountSchema + `,"assigned_to":` + restAccountSchema + `,"closure_request_reviewer":` +
	restAccountSchema + `,"first_location_detected":` + secretLocationSchema + `,"links":` +
	linksSchema(secretAlertLinkKeys)

var secretDetailFields = []capability.Field{
	{Name: "provider", Description: "Provider of the secret"},
	{Name: "provider_slug", Description: "Provider slug, as the providers and exclude_providers filters take it"},
	{Name: "is_base64_encoded", Description: "Whether the secret was found base64 encoded"},
	{Name: "has_more_locations", Description: "Whether the secret was found in more than one location"},
	{Name: "push_protection_bypassed_by", Description: "Account that bypassed push protection: " + restAccountMembers},
	{Name: "push_protection_bypassed_at", Description: "When push protection was bypassed"},
	{Name: "push_protection_bypass_request_reviewer", Description: "Account that reviewed the bypass request: " +
		restAccountMembers},
	{Name: "resolved_by_detail", Description: "Account that resolved the alert: " + restAccountMembers},
	{Name: "assigned_to", Description: "Account the alert is assigned to: " + restAccountMembers},
	{Name: "closure_request_reviewer", Description: "Account that reviewed the closure request: " + restAccountMembers},
	{Name: "first_location_detected", Description: "Where the secret was first detected: path, start_line, end_line, " +
		"start_column, end_column, blob_sha, commit_sha, url (the web address), and links (the REST and the " +
		"other *_url addresses of the location); never the found text; untrusted data, a cut path says so in " +
		"path_truncated"},
	{Name: "links", Description: "REST url, locations_url, and push_protection_bypass_request_html_url of the alert " +
		"by their GitHub names, untrusted data, never fetched"},
}

// ---- List filters

// secretFilters holds the secret scanning list filters; assignee is read apart because the Dependabot list
// takes a list under that name.
type secretFilters struct {
	ExcludeSecretTypes []string `json:"exclude_secret_types"`
	Providers          []string `json:"providers"`
	ExcludeProviders   []string `json:"exclude_providers"`
	Assignee           string   `json:"-"`
	Validity           []string `json:"validity"`
	IsPubliclyLeaked   *bool    `json:"is_publicly_leaked"`
	IsMultiRepo        *bool    `json:"is_multi_repo"`
	IsBypassed         *bool    `json:"is_bypassed"`
	IncludedMetadata   []string `json:"included_metadata"`
	OwnerEmailHash     string   `json:"owner_email_hash"`
}

// secretListArguments reads the arguments of the secret scanning list, whose assignee is a single string.
type secretListArguments struct {
	alertArguments
	secretFilters
	Assignee string `json:"assignee"`
}

func slugListSchema() string {
	return `{"type":"array","minItems":1,"maxItems":` + fmt.Sprint(maxSecretValues) + `,"uniqueItems":true,` +
		`"items":{"type":"string","minLength":1,"maxLength":` + fmt.Sprint(secretSlugLimit) + `,"pattern":"^[A-Za-z0-9_]+$"}}`
}

var secretFilterSchema = `"exclude_secret_types":` + slugListSchema() + `,"providers":` + slugListSchema() +
	`,"exclude_providers":` + slugListSchema() + `,"assignee":{"type":"string","minLength":1,"maxLength":100,` +
	`"pattern":"` + alertAssigneesReg + `"},"validity":` + enumListSchema(secretValidities) +
	`,"is_publicly_leaked":{"type":"boolean"},"is_multi_repo":{"type":"boolean"},"is_bypassed":{"type":"boolean"},` +
	`"included_metadata":` + enumListSchema(secretMetadata) + `,"owner_email_hash":{"type":"string",` +
	`"minLength":64,"maxLength":64,"pattern":"^[0-9a-f]{64}$"},"sort":` + enumSchema(codeScanningSorts) +
	`,"direction":` + enumSchema(codeScanningDirections)

var secretFilterArguments = []capability.Argument{
	{Name: "exclude_secret_types", Description: "Leave out alerts of these secret types, at most 20 type names; " +
		"not together with secret_type"},
	{Name: "providers", Description: "Return only alerts of these provider slugs, at most 20; not together with " +
		"exclude_providers"},
	{Name: "exclude_providers", Description: "Leave out alerts of these provider slugs, at most 20; not together " +
		"with providers"},
	{Name: "assignee", Description: "Return only alerts assigned to this login; * returns alerts with an " +
		"assignee and none alerts with none"},
	{Name: "validity", Description: "Return only alerts of these validities: " + strings.Join(secretValidities, ", ")},
	{Name: "is_publicly_leaked", Description: "True returns only publicly leaked secrets, false adds no filter"},
	{Name: "is_multi_repo", Description: "True returns only secrets found in several repositories, false adds no filter"},
	{Name: "is_bypassed", Description: "True returns only alerts caused by a bypassed push protection, false only " +
		"the others"},
	{Name: "included_metadata", Description: "Return only alerts that have all of these metadata attached: " +
		strings.Join(secretMetadata, ", ") + "; the metadata itself is not reported"},
	{Name: "owner_email_hash", Description: "Return only alerts whose owner email metadata matches this SHA-256 " +
		"hash, 64 lowercase hex characters; the metadata itself is not reported"},
	{Name: "sort", Description: "Sort by " + strings.Join(codeScanningSorts, " or ") + "; created when omitted"},
	{Name: "direction", Description: "Sort direction, " + strings.Join(codeScanningDirections, " or ") +
		"; desc when omitted"},
}

// hasSecretFilters reports whether a request sets a filter beyond state, secret_type, and resolution.
func (a *alertArguments) hasSecretFilters() bool {
	f := a.secret
	return len(f.ExcludeSecretTypes) > 0 || len(f.Providers) > 0 || len(f.ExcludeProviders) > 0 || f.Assignee != "" ||
		len(f.Validity) > 0 || f.IsPubliclyLeaked != nil || f.IsMultiRepo != nil || f.IsBypassed != nil ||
		len(f.IncludedMetadata) > 0 || f.OwnerEmailHash != "" || a.Sort != "" || a.Direction != ""
}

// checkSecretFilters rejects a value or combination GitHub would not take, before any credential is resolved.
func (a *alertArguments) checkSecretFilters() error {
	f := a.secret
	for _, list := range []struct {
		name   string
		values []string
	}{{"exclude_secret_types", f.ExcludeSecretTypes}, {"providers", f.Providers},
		{"exclude_providers", f.ExcludeProviders}} {
		if len(list.values) > maxSecretValues {
			return invalidRequest(fmt.Sprintf("%s accepts at most %d values", list.name, maxSecretValues))
		}
		for _, value := range list.values {
			if utf8.RuneCountInString(value) > secretSlugLimit || !secretSlugReg.MatchString(value) {
				return invalidRequest(list.name + " must be names of letters, digits, and underscores")
			}
		}
	}
	if len(f.ExcludeSecretTypes) > 0 && a.SecretType != "" {
		return invalidRequest("exclude_secret_types and secret_type cannot be combined")
	}
	if len(f.Providers) > 0 && len(f.ExcludeProviders) > 0 {
		return invalidRequest("providers and exclude_providers cannot be combined")
	}
	if f.Assignee != "" && f.Assignee != "*" && !validLogin(f.Assignee) {
		return invalidRequest("assignee must be a GitHub login, or * or none")
	}
	for _, list := range []struct {
		name    string
		values  []string
		allowed []string
	}{{"validity", f.Validity, secretValidities}, {"included_metadata", f.IncludedMetadata, secretMetadata}} {
		seen := map[string]bool{}
		for _, value := range list.values {
			if !containsFold(list.allowed, value) || seen[strings.ToLower(value)] {
				return invalidRequest(list.name + " must list each of " + strings.Join(list.allowed, ", ") + " at most once")
			}
			seen[strings.ToLower(value)] = true
		}
	}
	if f.OwnerEmailHash != "" && !ownerEmailHashReg.MatchString(f.OwnerEmailHash) {
		return invalidRequest("owner_email_hash must be 64 lowercase hex characters")
	}
	if a.Sort != "" && !containsFold(codeScanningSorts, a.Sort) {
		return invalidRequest("sort must be one of " + strings.Join(codeScanningSorts, ", "))
	}
	if a.Direction != "" && !containsFold(codeScanningDirections, a.Direction) {
		return invalidRequest("direction must be one of " + strings.Join(codeScanningDirections, ", "))
	}
	return nil
}

func boolText(p *bool) string {
	switch {
	case p == nil:
		return ""
	case *p:
		return "true"
	}
	return "false"
}

// secretBinding lists the filter values the cursor binds; a request without these filters binds nothing.
func (a *alertArguments) secretBinding() []any {
	f := a.secret
	return []any{strings.Join(f.ExcludeSecretTypes, ","), strings.Join(f.Providers, ","),
		strings.Join(f.ExcludeProviders, ","), strings.ToLower(f.Assignee), lowerJoin(f.Validity),
		boolText(f.IsPubliclyLeaked), boolText(f.IsMultiRepo), boolText(f.IsBypassed), lowerJoin(f.IncludedMetadata),
		f.OwnerEmailHash, strings.ToLower(a.Sort), strings.ToLower(a.Direction)}
}

func (a *alertArguments) secretQuery() []string {
	f := a.secret
	return []string{"exclude_secret_types", strings.Join(f.ExcludeSecretTypes, ","),
		"providers", strings.Join(f.Providers, ","), "exclude_providers", strings.Join(f.ExcludeProviders, ","),
		"assignee", strings.ToLower(f.Assignee), "validity", lowerJoin(f.Validity),
		"is_publicly_leaked", boolText(f.IsPubliclyLeaked), "is_multi_repo", boolText(f.IsMultiRepo),
		"is_bypassed", boolText(f.IsBypassed), "included_metadata", lowerJoin(f.IncludedMetadata),
		"owner_email_hash", f.OwnerEmailHash, "sort", strings.ToLower(a.Sort), "direction", strings.ToLower(a.Direction)}
}

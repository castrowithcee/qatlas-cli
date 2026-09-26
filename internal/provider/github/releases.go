package github

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// GitHub releases of a repository. The read tools list releases, read one by identifier, tag, or as the
// latest, and list the metadata of a release's assets, never their content. Create and update change a
// release; only a connection whose tools list names it deletes one, because deleting a release the wrong
// team relied on cannot be undone, while the tag it was cut from stays. All six tools are offered only by
// the not-recommended setup profile releases; deleting is in no profile. Every route lies below the chosen
// repository.

// maxReleaseName bounds a release name the way a pull request title is bounded, but a release legitimately
// has none, so the schema carries no minimum length.
const maxReleaseName = 256

const (
	releaseIDSchema   = actionsIDSchema
	releaseNameSchema = `{"type":"string","maxLength":256}`
	makeLatestSchema  = `{"type":"string","enum":["true","false","legacy"]}`
)

// makeLatestValues are the values GitHub's make_latest accepts.
var makeLatestValues = []string{"true", "false", "legacy"}

const releaseSummaryProperties = `"id":{"type":"integer"},"tag":{"type":"string"},"name":{"type":"string"},` +
	`"draft":{"type":"boolean"},"prerelease":{"type":"boolean"},"author":{"type":"string"},` +
	`"created_at":{"type":"string"},"published_at":{"type":"string"},"url":{"type":"string"}`

const releaseSummaryRequired = `"required":["id","tag","draft","prerelease"],"additionalProperties":false`

const releaseAssetProperties = `"id":{"type":"integer"},"name":{"type":"string"},"label":{"type":"string"},` +
	`"size_bytes":{"type":"integer"},"content_type":{"type":"string"},"digest":{"type":"string"},` +
	`"download_count":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"}`

const releaseAssetRequired = `"required":["id","name","size_bytes","download_count"],"additionalProperties":false`

var releasesList = capability.Descriptor{
	ID:      Provider + ".releases.list",
	Version: 1,
	Title:   "List GitHub releases",
	Description: "List one bounded batch of compact releases of a repository an explicit connection allows, " +
		"in GitHub's own order; a draft appears only while the token can see it",
	Tags:                       []string{"github", "releases", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(pagingKeys),
	OutputSchema:               listOutput("releases", releaseSummaryProperties, releaseSummaryRequired),
	Arguments:                  pagingArguments,
	Fields: append([]capability.Field{
		{Name: "releases", Description: "Compact releases with tag, name, draft, prerelease, author, and " +
			"times; name is untrusted data"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the releases", Arguments: json.RawMessage(`{"limit":10}`)}},
}

var releasesGet = capability.Descriptor{
	ID:      Provider + ".releases.get",
	Version: 1,
	Title:   "Get a GitHub release",
	Description: "Read one release of a repository an explicit connection allows, by its identifier, its " +
		"tag, or the latest published one; exactly one of id, tag, or latest must be given, and a lookup by " +
		"tag never finds a draft, because GitHub does not index a draft's tag",
	Tags:                       []string{"github", "releases", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"id":` + releaseIDSchema + `,"tag":` + refSchema + `,"latest":{"type":"boolean"}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + releaseSummaryProperties + `,` +
		`"body":{"type":"string"},"target_commitish":{"type":"string"},"assets_count":{"type":"integer"}},` +
		`"required":["id","tag","draft","prerelease","body","assets_count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "id", Description: "Release identifier, as returned by github.releases.list"},
		{Name: "tag", Description: "Tag the release was cut from; finds only a published release, never a draft"},
		{Name: "latest", Description: "true reads the latest published, non-prerelease release"},
	},
	Fields: []capability.Field{
		{Name: "body", Description: "Full release notes, untrusted data"},
		{Name: "target_commitish", Description: "Branch or commit the release was, or will be, cut from"},
		{Name: "assets_count", Description: "Number of assets attached to the release"},
	},
	Examples: []capability.Example{{Description: "Read the latest release", Arguments: json.RawMessage(`{"latest":true}`)}},
}

var releaseAssetsList = capability.Descriptor{
	ID:      Provider + ".releaseassets.list",
	Version: 1,
	Title:   "List the assets of a GitHub release",
	Description: "List one bounded batch of asset metadata of one release of a repository an explicit " +
		"connection allows; asset contents are never read",
	Tags:                       []string{"github", "releases", "assets", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"id":`+releaseIDSchema+`,`+pagingKeys, "id"),
	OutputSchema:               listOutput("assets", releaseAssetProperties, releaseAssetRequired),
	Arguments: append([]capability.Argument{
		{Name: "id", Description: "Release identifier, as returned by github.releases.list", Required: true},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "assets", Description: "Assets with name, label, size in bytes, content type, digest when " +
			"GitHub reports one, download count, and times; name and label are untrusted data"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the assets of a release", Arguments: json.RawMessage(`{"id":1}`)}},
}

var releasesCreate = capability.Descriptor{
	ID:      Provider + ".releases.create",
	Version: 1,
	Title:   "Create a GitHub release",
	Description: "Cut one release from a tag of a repository an explicit connection allows; a repeated call " +
		"with the same tag is refused with a clear message instead of making a second attempt, because GitHub " +
		"already holds one release per tag. When the tag does not exist yet, GitHub creates it at target when " +
		"the release is published",
	Tags:                       []string{"github", "releases", "create"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"tag":`+refSchema+`,"target":`+refSchema+`,"name":`+releaseNameSchema+`,`+
		`"body":`+bodySchema+`,"draft":{"type":"boolean"},"prerelease":{"type":"boolean"},`+
		`"generate_notes":{"type":"boolean"},"make_latest":`+makeLatestSchema, "tag"),
	OutputSchema: releasesGet.OutputSchema,
	Arguments: []capability.Argument{
		{Name: "tag", Description: "Tag to cut the release from; GitHub creates it at target if it does not " +
			"exist yet", Required: true},
		{Name: "target", Description: "Branch or commit the tag is created at when it does not exist yet; " +
			"the repository's default branch when omitted"},
		{Name: "name", Description: "Release name, at most 256 characters, untrusted data; the tag when omitted"},
		{Name: "body", Description: "Release notes in Markdown, at most 65536 characters; stored as given"},
		{Name: "draft", Description: "true creates it unpublished; false when omitted"},
		{Name: "prerelease", Description: "true marks it a prerelease; false when omitted"},
		{Name: "generate_notes", Description: "true has GitHub generate notes from merged pull requests and " +
			"append them to body"},
		{Name: "make_latest", Description: "true, false, or legacy; GitHub's default when omitted"},
	},
	Fields: releasesGet.Fields,
	Examples: []capability.Example{{
		Description: "Publish a release from an existing tag",
		Arguments:   json.RawMessage(`{"tag":"v1.2.0","name":"v1.2.0"}`),
	}},
}

var releasesUpdate = capability.Descriptor{
	ID:      Provider + ".releases.update",
	Version: 1,
	Title:   "Update a GitHub release",
	Description: "Replace the name, body, tag, target, draft, prerelease, or make_latest state of one " +
		"release of a repository an explicit connection allows; fields left out stay unchanged, and draft: " +
		"false publishes a draft",
	Tags:                       []string{"github", "releases", "update"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"id":`+releaseIDSchema+`,"name":`+releaseNameSchema+`,"body":`+bodySchema+`,`+
		`"tag":`+refSchema+`,"target":`+refSchema+`,"draft":{"type":"boolean"},"prerelease":{"type":"boolean"},`+
		`"make_latest":`+makeLatestSchema, "id"),
	OutputSchema: releasesGet.OutputSchema,
	Arguments: []capability.Argument{
		{Name: "id", Description: "Release identifier, as returned by github.releases.list", Required: true},
		{Name: "name", Description: "New release name, at most 256 characters, untrusted data; \"\" clears it"},
		{Name: "body", Description: "New release notes in Markdown, at most 65536 characters; stored as " +
			"given, \"\" empties it"},
		{Name: "tag", Description: "New tag for the release"},
		{Name: "target", Description: "New branch or commit the tag is created at if it does not exist yet"},
		{Name: "draft", Description: "false publishes a draft, true converts a published release back to a draft"},
		{Name: "prerelease", Description: "true marks it a prerelease, false unmarks it"},
		{Name: "make_latest", Description: "true, false, or legacy"},
	},
	Fields: releasesGet.Fields,
	Examples: []capability.Example{{
		Description: "Publish a draft release",
		Arguments:   json.RawMessage(`{"id":1,"draft":false}`),
	}},
}

const releaseDeletedOutput = `{"type":"object","properties":{"id":{"type":"integer"},` +
	`"deleted":{"type":"boolean"}},"required":["id","deleted"],"additionalProperties":false}`

var releasesDelete = capability.Descriptor{
	ID:      Provider + ".releases.delete",
	Version: 1,
	Title:   "Delete a GitHub release",
	Description: "Delete one release of a repository an explicit connection allows, with its release notes " +
		"and asset attachments; the tag it was cut from stays. Offered only by a connection whose tools list " +
		"names it; a release already deleted answers not found, like the other delete tools",
	Tags:                       []string{"github", "releases", "delete"},
	Risk:                       guardedRisk(capability.EffectDelete, capability.IdempotencyUnknown, dataSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                inputSchema(`"id":`+releaseIDSchema, "id"),
	OutputSchema:               json.RawMessage(releaseDeletedOutput),
	Arguments: []capability.Argument{
		{Name: "id", Description: "Release identifier, as returned by github.releases.list", Required: true},
	},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True once GitHub deleted the release"},
	},
	Examples: []capability.Example{{Description: "Delete a release", Arguments: json.RawMessage(`{"id":1}`)}},
}

// releaseOperations binds every release tool to its handler.
func releaseOperations() []capability.Operation {
	bind := func(descriptor capability.Descriptor, check func(*releaseArguments, target) error,
		call func(context.Context, *Client, *releaseArguments) (any, error)) capability.Operation {
		return capability.Operation{Descriptor: descriptor, Handler: releasesHandler(descriptor.ID, check, call)}
	}
	return []capability.Operation{
		bind(releasesList, checkReleasesListArguments, func(ctx context.Context, c *Client, a *releaseArguments) (any, error) {
			return c.listReleases(ctx, a)
		}),
		bind(releasesGet, checkReleasesGetArguments, func(ctx context.Context, c *Client, a *releaseArguments) (any, error) {
			return c.getRelease(ctx, a)
		}),
		bind(releaseAssetsList, checkReleaseAssetsListArguments, func(ctx context.Context, c *Client, a *releaseArguments) (any, error) {
			return c.listReleaseAssets(ctx, a)
		}),
		bind(releasesCreate, checkReleasesCreateArguments, func(ctx context.Context, c *Client, a *releaseArguments) (any, error) {
			return c.createRelease(ctx, a)
		}),
		bind(releasesUpdate, checkReleasesUpdateArguments, func(ctx context.Context, c *Client, a *releaseArguments) (any, error) {
			return c.updateRelease(ctx, a)
		}),
		bind(releasesDelete, checkReleaseIDArgument, func(ctx context.Context, c *Client, a *releaseArguments) (any, error) {
			return c.deleteRelease(ctx, a.ID)
		}),
	}
}

// releaseArguments holds the arguments of every release tool; the input schema of each tool admits only its
// own. page and perPage are derived by the checks. Name, body, draft, and prerelease are pointers, so a value
// left out stays apart from an explicit false or "": no release field this provider writes accepts those as
// an unset value.
type releaseArguments struct {
	ID     int64  `json:"id"`
	Tag    string `json:"tag"`
	Latest bool   `json:"latest"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`

	// The further arguments of the change tools.
	Target        string  `json:"target"`
	Name          *string `json:"name"`
	Body          *string `json:"body"`
	Draft         *bool   `json:"draft"`
	Prerelease    *bool   `json:"prerelease"`
	GenerateNotes bool    `json:"generate_notes"`
	MakeLatest    string  `json:"make_latest"`

	page, perPage int
	binding       []byte
}

// releasesHandler decodes and checks the arguments and the repository before a credential is resolved, so a
// refused request never becomes a provider call.
func releasesHandler(id string, check func(*releaseArguments, target) error,
	call func(context.Context, *Client, *releaseArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments releaseArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if err := check(&arguments, bound); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, &arguments))
	}
}

func checkReleaseIDArgument(a *releaseArguments, _ target) error {
	if a.ID < 1 {
		return invalidRequest("id must be a positive release identifier")
	}
	return nil
}

func checkReleasesListArguments(a *releaseArguments, bound target) error {
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	a.binding = fingerprint("releases", "list", bound.String())
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

func checkReleaseAssetsListArguments(a *releaseArguments, bound target) error {
	if err := checkReleaseIDArgument(a, bound); err != nil {
		return err
	}
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	a.binding = fingerprint("releases", "assets", bound.String(), a.ID)
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

func checkReleasesGetArguments(a *releaseArguments, _ target) error {
	given := 0
	for _, selected := range []bool{a.ID != 0, a.Tag != "", a.Latest} {
		if selected {
			given++
		}
	}
	if given != 1 {
		return invalidRequest("give exactly one of id, tag, or latest")
	}
	if a.Tag != "" && !validRef(a.Tag) {
		return invalidRequest("tag must be a usable ref name")
	}
	return nil
}

func checkMakeLatest(value string) error {
	if value != "" && !containsFold(makeLatestValues, value) {
		return invalidRequest("make_latest must be true, false, or legacy")
	}
	return nil
}

func checkReleasesCreateArguments(a *releaseArguments, _ target) error {
	if !validRef(a.Tag) {
		return invalidRequest("tag must be a usable ref name")
	}
	if a.Target != "" && !validRef(a.Target) {
		return invalidRequest("target must be a branch name or a commit SHA")
	}
	if a.Name != nil {
		if err := checkBoundedText("name", *a.Name, maxReleaseName); err != nil {
			return err
		}
	}
	if a.Body != nil {
		if err := checkBoundedText("body", *a.Body, maxBodyLength); err != nil {
			return err
		}
	}
	return checkMakeLatest(a.MakeLatest)
}

func checkReleasesUpdateArguments(a *releaseArguments, bound target) error {
	if err := checkReleaseIDArgument(a, bound); err != nil {
		return err
	}
	if a.Name == nil && a.Body == nil && a.Tag == "" && a.Target == "" && a.Draft == nil && a.Prerelease == nil &&
		a.MakeLatest == "" {
		return invalidRequest("name at least one of name, body, tag, target, draft, prerelease, or make_latest to change")
	}
	if a.Tag != "" && !validRef(a.Tag) {
		return invalidRequest("tag must be a usable ref name")
	}
	if a.Target != "" && !validRef(a.Target) {
		return invalidRequest("target must be a branch name or a commit SHA")
	}
	if a.Name != nil {
		if err := checkBoundedText("name", *a.Name, maxReleaseName); err != nil {
			return err
		}
	}
	if a.Body != nil {
		if err := checkBoundedText("body", *a.Body, maxBodyLength); err != nil {
			return err
		}
	}
	return checkMakeLatest(a.MakeLatest)
}

func (a *releaseArguments) query() url.Values {
	return url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
}

// createPayload is the REST body a create sends. tag is always sent; every other field travels only when
// given, so GitHub's own defaults apply to the rest.
func (a *releaseArguments) createPayload() map[string]any {
	payload := map[string]any{"tag_name": a.Tag}
	if a.Target != "" {
		payload["target_commitish"] = a.Target
	}
	if a.Name != nil {
		payload["name"] = *a.Name
	}
	if a.Body != nil {
		payload["body"] = *a.Body
	}
	if a.Draft != nil {
		payload["draft"] = *a.Draft
	}
	if a.Prerelease != nil {
		payload["prerelease"] = *a.Prerelease
	}
	if a.GenerateNotes {
		payload["generate_release_notes"] = true
	}
	if a.MakeLatest != "" {
		payload["make_latest"] = a.MakeLatest
	}
	return payload
}

// updatePayload is the REST body an update sends: only the fields set travel, so a field left out stays.
func (a *releaseArguments) updatePayload() map[string]any {
	payload := map[string]any{}
	if a.Name != nil {
		payload["name"] = *a.Name
	}
	if a.Body != nil {
		payload["body"] = *a.Body
	}
	if a.Tag != "" {
		payload["tag_name"] = a.Tag
	}
	if a.Target != "" {
		payload["target_commitish"] = a.Target
	}
	if a.Draft != nil {
		payload["draft"] = *a.Draft
	}
	if a.Prerelease != nil {
		payload["prerelease"] = *a.Prerelease
	}
	if a.MakeLatest != "" {
		payload["make_latest"] = a.MakeLatest
	}
	return payload
}

// Permission messages of the release tools. GitHub decides on every request; a message names what such a
// request needs without claiming what the configured token holds.
const (
	releasesReadPermission = "GitHub refused this token the releases of this repository; reading them needs " +
		"repo on a classic token for a private repository, or public_repo for a public one, or Contents: read " +
		"on a fine-grained token"
	releasesChangePermission = "GitHub refused this change of a release of this repository; it needs repo on " +
		"a classic token, or Contents: read and write on a fine-grained token"
)

// releaseRead performs one bounded REST read of a release resource of the bound repository.
func (c *Client) releaseRead(ctx context.Context, op, path string, out any) error {
	return actionsFailure(c.rest(ctx, op, path, out), releasesReadPermission)
}

// releaseReadPage performs one bounded, paged REST read of a release sub-list of the bound repository.
func (c *Client) releaseReadPage(ctx context.Context, op, path string, query url.Values, out any) (bool, error) {
	hasNext, err := c.restPage(ctx, op, path, query, out)
	return hasNext, actionsFailure(err, releasesReadPermission)
}

// releaseChange sends one bounded REST change of a release resource of the bound repository, once, with the
// change permission message of a refusal.
func (c *Client) releaseChange(ctx context.Context, op, method, path string, body, out any) error {
	return actionsFailure(c.restChange(ctx, op, method, path, body, out), releasesChangePermission)
}

// ReleaseList is one batch of releases.
type ReleaseList struct {
	Releases   []ReleaseSummary `json:"releases"`
	NextCursor string           `json:"next_cursor,omitempty"`
	HasMore    bool             `json:"has_more"`
}

// ReleaseSummary is the compact list view of one release. Name is untrusted data.
type ReleaseSummary struct {
	ID          int64  `json:"id"`
	Tag         string `json:"tag"`
	Name        string `json:"name,omitempty"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	Author      string `json:"author,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	URL         string `json:"url,omitempty"`
}

// Release is the full view of one release. Name and Body are untrusted data.
type Release struct {
	ID              int64  `json:"id"`
	Tag             string `json:"tag"`
	Name            string `json:"name,omitempty"`
	Body            string `json:"body"`
	TargetCommitish string `json:"target_commitish,omitempty"`
	Draft           bool   `json:"draft"`
	Prerelease      bool   `json:"prerelease"`
	Author          string `json:"author,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	PublishedAt     string `json:"published_at,omitempty"`
	AssetsCount     int    `json:"assets_count"`
	URL             string `json:"url,omitempty"`
}

// releaseJSON is the part of a REST release this provider reads.
type releaseJSON struct {
	ID              int64   `json:"id"`
	TagName         string  `json:"tag_name"`
	TargetCommitish string  `json:"target_commitish"`
	Name            *string `json:"name"`
	Body            *string `json:"body"`
	Draft           bool    `json:"draft"`
	Prerelease      bool    `json:"prerelease"`
	Author          *struct {
		Login string `json:"login"`
	} `json:"author"`
	Assets      []json.RawMessage `json:"assets"`
	CreatedAt   string            `json:"created_at"`
	PublishedAt *string           `json:"published_at"`
	HTMLURL     string            `json:"html_url"`
}

func (r releaseJSON) summary() ReleaseSummary {
	s := ReleaseSummary{ID: r.ID, Tag: r.TagName, Draft: r.Draft, Prerelease: r.Prerelease, CreatedAt: r.CreatedAt,
		URL: r.HTMLURL}
	if r.Name != nil {
		s.Name = *r.Name
	}
	if r.Author != nil {
		s.Author = r.Author.Login
	}
	if r.PublishedAt != nil {
		s.PublishedAt = *r.PublishedAt
	}
	return s
}

func (r releaseJSON) full() *Release {
	s := r.summary()
	full := &Release{ID: s.ID, Tag: s.Tag, Name: s.Name, TargetCommitish: r.TargetCommitish, Draft: s.Draft,
		Prerelease: s.Prerelease, Author: s.Author, CreatedAt: s.CreatedAt, PublishedAt: s.PublishedAt,
		AssetsCount: len(r.Assets), URL: s.URL}
	if r.Body != nil {
		full.Body = *r.Body
	}
	return full
}

func (c *Client) listReleases(ctx context.Context, a *releaseArguments) (*ReleaseList, error) {
	const op = "list releases"
	var raw []releaseJSON
	hasNext, err := c.releaseReadPage(ctx, op, c.repoPath("releases"), a.query(), &raw)
	if err != nil {
		return nil, err
	}
	result := &ReleaseList{Releases: make([]ReleaseSummary, 0, len(raw))}
	for _, release := range raw {
		if release.ID < 1 {
			return nil, invalidEntry(op, "a release")
		}
		result.Releases = append(result.Releases, release.summary())
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// getRelease reads one release of the bound repository by identifier, tag, or as the latest published one;
// checkReleasesGetArguments already ensured exactly one selector is given.
func (c *Client) getRelease(ctx context.Context, a *releaseArguments) (*Release, error) {
	const op = "get release"
	var path string
	switch {
	case a.ID != 0:
		path = c.repoPath("releases/" + strconv.FormatInt(a.ID, 10))
	case a.Tag != "":
		path = c.repoPath("releases/tags/" + url.PathEscape(a.Tag))
	default:
		path = c.repoPath("releases/latest")
	}
	var raw releaseJSON
	if err := c.releaseRead(ctx, op, path, &raw); err != nil {
		return nil, err
	}
	if raw.ID < 1 {
		return nil, invalidEntry(op, "a release")
	}
	return raw.full(), nil
}

// ReleaseAssetList is one batch of the asset metadata of a release.
type ReleaseAssetList struct {
	Assets     []ReleaseAsset `json:"assets"`
	NextCursor string         `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
}

// ReleaseAsset is the metadata of one release asset. Its content is never read. Name and Label are untrusted
// data.
type ReleaseAsset struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentType string `json:"content_type,omitempty"`
	Digest      string `json:"digest,omitempty"`
	Downloads   int64  `json:"download_count"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

type releaseAssetJSON struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Label         string `json:"label"`
	Size          int64  `json:"size"`
	ContentType   string `json:"content_type"`
	Digest        string `json:"digest"`
	DownloadCount int64  `json:"download_count"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

func (a releaseAssetJSON) view() ReleaseAsset {
	return ReleaseAsset{ID: a.ID, Name: a.Name, Label: a.Label, SizeBytes: a.Size, ContentType: a.ContentType,
		Digest: a.Digest, Downloads: a.DownloadCount, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
}

func (c *Client) listReleaseAssets(ctx context.Context, a *releaseArguments) (*ReleaseAssetList, error) {
	const op = "list release assets"
	var raw []releaseAssetJSON
	path := c.repoPath("releases/" + strconv.FormatInt(a.ID, 10) + "/assets")
	hasNext, err := c.releaseReadPage(ctx, op, path, a.query(), &raw)
	if err != nil {
		return nil, err
	}
	result := &ReleaseAssetList{Assets: make([]ReleaseAsset, 0, len(raw))}
	for _, asset := range raw {
		if asset.ID < 1 {
			return nil, invalidEntry(op, "a release asset")
		}
		result.Assets = append(result.Assets, asset.view())
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// createRelease sends the create request directly, instead of through restChange, so a 422 answer can be
// read once and, when it names an existing release for the tag, turned into a clear refusal instead of the
// generic "rejected as invalid" message; the request is still sent exactly once. Every other status is
// classified exactly like restChange would.
func (c *Client) createRelease(ctx context.Context, a *releaseArguments) (*Release, error) {
	const op = "create release"
	payload, err := json.Marshal(a.createPayload())
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "GitHub", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoints.rest+c.repoPath("releases"),
		bytes.NewReader(payload))
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	c.authorize(req)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	defer c.limiter.HoldFor(mutationInterval)
	if err != nil {
		failure := provider.Transport(op, "GitHub", err)
		if failure.Class == provider.ClassTimeout || failure.Cause == provider.CauseConnectionReset ||
			failure.Cause == provider.CauseUnknown {
			failure.Message += uncertain
		}
		return nil, failure
	}
	defer response.Body.Close()
	c.observeRateLimit(response.Header)
	if response.StatusCode < 200 || response.StatusCode > 299 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if response.StatusCode == http.StatusUnprocessableEntity && bytes.Contains(data, []byte(`"already_exists"`)) {
			return nil, invalidRequest("GitHub already holds a release for tag " + a.Tag +
				"; update or delete that release instead of creating another")
		}
		response.Body = io.NopCloser(bytes.NewReader(data))
		return nil, actionsFailure(c.statusError(op, response, true), releasesChangePermission)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "the GitHub response could not be read within the size limit" + uncertain}
	}
	var raw releaseJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, invalidResponse(op, true)
	}
	return raw.full(), nil
}

func (c *Client) updateRelease(ctx context.Context, a *releaseArguments) (*Release, error) {
	const op = "update release"
	var raw releaseJSON
	path := c.repoPath("releases/" + strconv.FormatInt(a.ID, 10))
	if err := c.releaseChange(ctx, op, http.MethodPatch, path, a.updatePayload(), &raw); err != nil {
		return nil, err
	}
	return raw.full(), nil
}

// DeletedRelease is the answer of a deleted release.
type DeletedRelease struct {
	ID      int64 `json:"id"`
	Deleted bool  `json:"deleted"`
}

// deleteRelease deletes one release of the bound repository. GitHub answers with no body, so no out is
// decoded; a release already deleted answers 404, which becomes the usual not-found refusal naming it.
func (c *Client) deleteRelease(ctx context.Context, id int64) (*DeletedRelease, error) {
	const op = "delete release"
	path := c.endpoints.rest + c.repoPath("releases/"+strconv.FormatInt(id, 10))
	if err := c.do(ctx, op, http.MethodDelete, path, nil, nil, true, nil); err != nil {
		return nil, actionsFailure(err, releasesChangePermission)
	}
	return &DeletedRelease{ID: id, Deleted: true}, nil
}

// releasesSubject names the release or the release asset collection a releases path below a repository
// addresses: releases/ID, releases/ID/assets, releases/tags/TAG, or releases/latest. It names only an
// identifier or a tag of the characters the input schema allows, and is empty otherwise.
func releasesSubject(path string) string {
	if rest, ok := strings.CutPrefix(path, "releases/tags/"); ok {
		if tag, err := url.PathUnescape(rest); err == nil && validRef(tag) {
			return "release with tag " + tag
		}
		return ""
	}
	rest, ok := strings.CutPrefix(path, "releases/")
	if !ok || rest == "" {
		return ""
	}
	if rest == "latest" {
		return "the latest release"
	}
	digits, tail, _ := strings.Cut(rest, "/")
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || id < 1 {
		return ""
	}
	if tail == "assets" {
		return "release asset " + digits
	}
	return "release " + digits
}

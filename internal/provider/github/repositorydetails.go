package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// The repository read tools answer one repository of the connection through fixed GitHub routes: its details,
// its languages, its contributors, and its community profile. They write nothing. Names, descriptions, and
// addresses GitHub reports are untrusted data and never fetched. A repository the details name besides the
// bound one, such as the parent of a fork, shows only identifiers unless the connection's targets admit it.

const (
	repositoryReadPermission = "GitHub refused this token the details of this repository; reading them needs " +
		"no scope for a public repository, or repo on a classic token, or Metadata: read on a fine-grained " +
		"token, for a private one"
	languagesReadPermission = "GitHub refused this token the languages of this repository; reading them needs " +
		"no scope for a public repository, or repo on a classic token, or Metadata: read on a fine-grained " +
		"token, for a private one"
	contributorsReadPermission = "GitHub refused this token the contributors of this repository; reading them " +
		"needs no scope for a public repository, or repo on a classic token, or Metadata: read on a " +
		"fine-grained token, for a private one"
	communityProfileReadPermission = "GitHub refused this token the community profile of this repository; " +
		"reading it needs no scope for a public repository, or repo on a classic token, or Metadata: read on a " +
		"fine-grained token, for a private one"
)

// Values of the state member of the statistics tools.
const (
	stateReady     = "ready"
	stateComputing = "computing"
	stateEmpty     = "empty"
)

var repositoryDetailsExtraProperties = `"parent":` + restRepositorySchema + `,"source":` + restRepositorySchema +
	`,"template_repository":` + restRepositorySchema + `,"organization":` + restAccountSchema

var repositoryDetailsFields = append(append([]capability.Field{}, restRepositoryFields...), []capability.Field{
	{Name: "parent", Description: "Repository this one was forked from, as a repository: the members above for " +
		"one the connection's targets admit, otherwise only id, node_id, and full_name"},
	{Name: "source", Description: "Root of the fork network, as a repository, bounded like parent"},
	{Name: "template_repository", Description: "Template this repository was generated from, as a repository, " +
		"bounded like parent"},
	{Name: "organization", Description: "Organization owning the repository, as an account: " + restAccountMembers},
}...)

var repositoriesGet = capability.Descriptor{
	ID:      Provider + ".repositories.get",
	Version: 1,
	Title:   "Get a GitHub repository",
	Description: "Read one repository a connection allows with every detail GitHub reports: settings, merge " +
		"options, counts, license, code of conduct, security and analysis status, custom properties, owner, " +
		"organization, and the parent, source, and template it links to, which show only identifiers when the " +
		"connection's targets do not admit them; the clone token is never shown",
	Tags:         []string{"github", "repositories", "get"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(``),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + restRepositoryProperties + `,` + repositoryDetailsExtraProperties + `},"required":["id","full_name"],"additionalProperties":false}`),
	Fields:       repositoryDetailsFields,
	Examples: []capability.Example{{
		Description: "Read a repository",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example"}`),
	}},
}

var languagesGet = capability.Descriptor{
	ID:      Provider + ".languages.get",
	Version: 1,
	Title:   "Get the languages of a GitHub repository",
	Description: "Read the languages of a repository a connection allows with the number of bytes of code in " +
		"each, largest first; GitHub answers 202 while it still computes them, reported as state computing " +
		"without waiting or repeating the request",
	Tags:        []string{"github", "repositories", "languages", "get"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(``),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"state":{"type":"string"},"languages":{` +
		`"type":"array","maxItems":100,"items":{"type":"object","properties":{"name":{"type":"string"},` +
		`"bytes":{"type":"integer"}},"required":["name","bytes"],"additionalProperties":false}},` +
		`"languages_truncated":{"type":"boolean"}},"required":["state","languages"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "state", Description: "ready, or computing when GitHub still computes the languages and sent none"},
		{Name: "languages", Description: "Languages with name (untrusted data) and bytes of code, largest first, " +
			"at most 100; empty while state is computing"},
		{Name: "languages_truncated", Description: "True when the repository holds more languages than shown"},
	},
	Examples: []capability.Example{{
		Description: "Read the languages of a repository",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example"}`),
	}},
}

var contributorProperties = restAccountProperties + `,"contributions":{"type":"integer"}`

var contributorsList = capability.Descriptor{
	ID:      Provider + ".contributors.list",
	Version: 1,
	Title:   "List GitHub repository contributors",
	Description: "List one bounded batch of the contributors of a repository a connection allows with their " +
		"number of commits, most commits first, optionally with anonymous contributors, who carry only a name " +
		"and an email address as untrusted data; GitHub caps the list at 500 contributors, answers 202 while it " +
		"still computes it, reported as state computing without waiting or repeating the request, and answers " +
		"an empty repository with no content, reported as state empty",
	Tags:        []string{"github", "repositories", "contributors", "list"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(`"anon":{"type":"boolean"},` + pagingKeys),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"state":{"type":"string"},"contributors":{` +
		`"type":"array","items":{"type":"object","properties":{` + contributorProperties + `},` +
		`"required":["contributions"],"additionalProperties":false}},"next_cursor":{"type":"string"},` +
		`"has_more":{"type":"boolean"}},"required":["state","contributors","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "anon", Description: "True to include anonymous contributors, who have no account; false when omitted"},
	}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "state", Description: "ready, computing when GitHub still computes the list and sent none, or " +
			"empty for a repository without commits"},
		{Name: "contributors", Description: "Contributors as an account (" + restAccountMembers + ") with " +
			"contributions; an anonymous contributor has type Anonymous and only name and email"},
		{Name: "contributions", Description: "Number of commits of the contributor"},
	}, restAccountFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "List contributors including anonymous ones",
		Arguments:   json.RawMessage(`{"anon":true,"limit":50}`),
	}},
}

var healthFileSchema = objectSchema(`"url":{"type":"string"},"links":` + linksSchema(healthFileLinkKeys))

var healthFileLinkKeys = []string{"url"}

var communityProfileGet = capability.Descriptor{
	ID:      Provider + ".community-profile.get",
	Version: 1,
	Title:   "Get the community profile of a GitHub repository",
	Description: "Read the community profile of a repository a connection allows: the health percentage, the " +
		"description and documentation address, which community files exist (code of conduct, license, " +
		"contributing, readme, issue and pull request templates), when it was last computed, and whether " +
		"content reports are enabled",
	Tags:        []string{"github", "repositories", "community-profile", "get"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(``),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"health_percentage":{"type":"integer"},` +
		`"description":{"type":"string"},"documentation":{"type":"string"},"files":` + objectSchema(
		`"code_of_conduct":`+objectSchema(`"key":{"type":"string"},"name":{"type":"string"},"url":{"type":"string"},`+
			`"links":`+linksSchema(codeOfConductLinkKeys))+`,"code_of_conduct_file":`+healthFileSchema+
			`,"license":`+objectSchema(`"key":{"type":"string"},"name":{"type":"string"},"spdx_id":{"type":"string"},`+
			`"node_id":{"type":"string"},"url":{"type":"string"},"links":`+linksSchema(licenseLinkKeys))+
			`,"contributing":`+healthFileSchema+`,"readme":`+healthFileSchema+`,"issue_template":`+healthFileSchema+
			`,"pull_request_template":`+healthFileSchema) + `,"updated_at":{"type":"string"},` +
		`"content_reports_enabled":{"type":"boolean"}},"required":["health_percentage","files"],` +
		`"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "health_percentage", Description: "Share of the recommended community files the repository has, " +
			"from 0 through 100"},
		{Name: "description", Description: "Repository description, untrusted data"},
		{Name: "documentation", Description: "Documentation address, untrusted data, never fetched"},
		{Name: "files", Description: "Community files by role: code_of_conduct (key, name, url, links), " +
			"code_of_conduct_file, license (key, name, spdx_id, node_id, url, links), contributing, readme, " +
			"issue_template, and pull_request_template, each file with url (the web address) and links (the " +
			"REST url); a role without a file is absent; all untrusted data, never fetched"},
		{Name: "updated_at", Description: "Time the profile was last computed"},
		{Name: "content_reports_enabled", Description: "True when content reports are enabled; GitHub sends it " +
			"only for a repository of an organization"},
	},
	Examples: []capability.Example{{
		Description: "Read the community profile of a repository",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example"}`),
	}},
}

func repositoryDetailOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: repositoriesGet, Handler: repositoryReadHandler(func(ctx context.Context, c *Client) (any, error) {
			return c.getRepository(ctx)
		})},
		{Descriptor: languagesGet, Handler: repositoryReadHandler(func(ctx context.Context, c *Client) (any, error) {
			return c.getLanguages(ctx)
		})},
		{Descriptor: communityProfileGet, Handler: repositoryReadHandler(func(ctx context.Context, c *Client) (any, error) {
			return c.getCommunityProfile(ctx)
		})},
		{Descriptor: contributorsList, Handler: capability.Handler(invokeContributorsList)},
	}
}

// repositoryReadHandler binds a tool without arguments beyond the repository: the target is checked against
// the connection's targets and exclusions before a credential is resolved.
func repositoryReadHandler(call func(context.Context, *Client) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client))
	}
}

// restStatus reads one REST route and reports the status code, since GitHub tells a computed answer from an
// accepted request or an empty one by status.
func (c *Client) restStatus(ctx context.Context, op, path string, query url.Values, out any) (int, bool, error) {
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var header http.Header
	var status int
	if err := c.do(ctx, op, http.MethodGet, c.endpoints.rest+path, nil, out, false, &header, &status); err != nil {
		return 0, false, err
	}
	return status, hasNextPage(header), nil
}

// ---- github.repositories.get

// repositoryDetailRaw is the REST full repository: the repository members and the objects it links to. The
// clone token has no member here, so it can never be emitted.
type repositoryDetailRaw struct {
	base               repositoryRaw
	Parent             *repositoryRaw `json:"parent"`
	Source             *repositoryRaw `json:"source"`
	TemplateRepository *repositoryRaw `json:"template_repository"`
	Organization       *accountRaw    `json:"organization"`
}

func (r *repositoryDetailRaw) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &r.base); err != nil {
		return err
	}
	type links struct {
		Parent             *repositoryRaw `json:"parent"`
		Source             *repositoryRaw `json:"source"`
		TemplateRepository *repositoryRaw `json:"template_repository"`
		Organization       *accountRaw    `json:"organization"`
	}
	var own links
	if err := json.Unmarshal(data, &own); err != nil {
		return err
	}
	r.Parent, r.Source, r.TemplateRepository, r.Organization = own.Parent, own.Source, own.TemplateRepository,
		own.Organization
	return nil
}

// RepositoryDetails is the view of one repository with the repositories and the organization it links to.
type RepositoryDetails struct {
	RESTRepository
	Parent             *RESTRepository `json:"parent,omitempty"`
	Source             *RESTRepository `json:"source,omitempty"`
	TemplateRepository *RESTRepository `json:"template_repository,omitempty"`
	Organization       *RESTAccount    `json:"organization,omitempty"`
}

func (c *Client) getRepository(ctx context.Context) (*RepositoryDetails, error) {
	const op = "get repository"
	var raw repositoryDetailRaw
	if err := c.rest(ctx, op, strings.TrimSuffix(c.repoPath(""), "/"), &raw); err != nil {
		return nil, actionsFailure(err, repositoryReadPermission)
	}
	if raw.base.ID == nil || raw.base.FullName == nil || *raw.base.FullName == "" {
		return nil, invalidEntry(op, "a repository")
	}
	return &RepositoryDetails{RESTRepository: *raw.base.project(c.allowed), Parent: raw.Parent.project(c.allowed),
		Source: raw.Source.project(c.allowed), TemplateRepository: raw.TemplateRepository.project(c.allowed),
		Organization: raw.Organization.project()}, nil
}

// ---- github.languages.get

// Language is one language of a repository with the bytes of code in it.
type Language struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// RepositoryLanguages are the languages of a repository, or the state computing while GitHub still works on
// them.
type RepositoryLanguages struct {
	State              string     `json:"state"`
	Languages          []Language `json:"languages"`
	LanguagesTruncated bool       `json:"languages_truncated,omitempty"`
}

func (c *Client) getLanguages(ctx context.Context) (*RepositoryLanguages, error) {
	const op = "get languages"
	var raw json.RawMessage
	status, _, err := c.restStatus(ctx, op, c.repoPath("languages"), nil, &raw)
	if err != nil {
		return nil, actionsFailure(err, languagesReadPermission)
	}
	result := &RepositoryLanguages{State: stateReady, Languages: []Language{}}
	if status == http.StatusAccepted {
		result.State = stateComputing
		return result, nil
	}
	var bytes map[string]int64
	if err := json.Unmarshal(raw, &bytes); err != nil {
		return nil, invalidResponse(op, false)
	}
	for name, count := range bytes {
		result.Languages = append(result.Languages, Language{Name: *nameText(&name), Bytes: count})
	}
	sort.Slice(result.Languages, func(i, j int) bool {
		a, b := result.Languages[i], result.Languages[j]
		return a.Bytes > b.Bytes || a.Bytes == b.Bytes && a.Name < b.Name
	})
	result.Languages, result.LanguagesTruncated = capped(result.Languages)
	return result, nil
}

// ---- github.contributors.list

type contributorListOptions struct {
	Anon   bool   `json:"anon"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`

	page, perPage int
	binding       []byte
}

func (o *contributorListOptions) query() url.Values {
	values := url.Values{"per_page": {strconv.Itoa(o.perPage)}, "page": {strconv.Itoa(o.page)}}
	if o.Anon {
		values.Set("anon", "true")
	}
	return values
}

func (o *contributorListOptions) normalize(bound target) error {
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return err
	}
	o.binding = fingerprint("contributors", "list", bound.String(), strconv.FormatBool(o.Anon))
	o.page, o.perPage, err = pageOf(o.binding, o.Cursor, limit)
	return err
}

// invokeContributorsList checks the target, the filter, and the cursor before a credential is resolved.
func invokeContributorsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options contributorListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, unreadable(contributorsList.ID)
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := options.normalize(bound); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.listContributors(ctx, &options))
}

type contributorRaw struct {
	account       accountRaw
	Contributions *int64 `json:"contributions"`
}

func (r *contributorRaw) UnmarshalJSON(data []byte) error {
	var own struct {
		Contributions *int64 `json:"contributions"`
	}
	if err := json.Unmarshal(data, &own); err != nil {
		return err
	}
	r.Contributions = own.Contributions
	return json.Unmarshal(data, &r.account)
}

// Contributor is the view of one contributor: an account, or an anonymous contributor with a name and an
// email address only, and the number of commits.
type Contributor struct {
	RESTAccount
	Contributions int64 `json:"contributions"`
}

// ContributorList is one batch of the contributors of a repository. State is computing or empty when GitHub
// sent no list.
type ContributorList struct {
	State        string        `json:"state"`
	Contributors []Contributor `json:"contributors"`
	NextCursor   string        `json:"next_cursor,omitempty"`
	HasMore      bool          `json:"has_more"`
}

func (c *Client) listContributors(ctx context.Context, o *contributorListOptions) (*ContributorList, error) {
	const op = "list contributors"
	var body json.RawMessage
	status, hasNext, err := c.restStatus(ctx, op, c.repoPath("contributors"), o.query(), &body)
	if err != nil {
		return nil, actionsFailure(err, contributorsReadPermission)
	}
	result := &ContributorList{State: stateReady, Contributors: []Contributor{}}
	switch status {
	case http.StatusAccepted:
		result.State = stateComputing
		return result, nil
	case http.StatusNoContent:
		result.State = stateEmpty
		return result, nil
	}
	var raw []contributorRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, invalidResponse(op, false)
	}
	for i := range raw {
		entry := &raw[i]
		a := entry.account
		if entry.Contributions == nil || a.Login == nil && a.Email == nil && a.Name == nil {
			return nil, invalidEntry(op, "a contributor")
		}
		result.Contributors = append(result.Contributors, Contributor{RESTAccount: *a.project(),
			Contributions: *entry.Contributions})
	}
	result.HasMore, result.NextCursor = morePage(o.binding, o.page, o.perPage, hasNext)
	return result, nil
}

// ---- github.community-profile.get

type healthFileRaw struct {
	HTMLURL *string `json:"html_url"`
	links   Links
}

func (r *healthFileRaw) UnmarshalJSON(data []byte) (err error) {
	var own struct {
		HTMLURL *string `json:"html_url"`
	}
	if err = json.Unmarshal(data, &own); err != nil {
		return err
	}
	r.HTMLURL = own.HTMLURL
	r.links, err = readLinks(data, healthFileLinkKeys)
	return err
}

// RESTHealthFile is a community file of a repository.
type RESTHealthFile struct {
	URL   *string `json:"url,omitempty"`
	Links Links   `json:"links,omitempty"`
}

func (r *healthFileRaw) project() *RESTHealthFile {
	if r == nil {
		return nil
	}
	return &RESTHealthFile{URL: address(r.HTMLURL), Links: r.links}
}

type communityProfileRaw struct {
	HealthPercentage *int64  `json:"health_percentage"`
	Description      *string `json:"description"`
	Documentation    *string `json:"documentation"`
	Files            *struct {
		CodeOfConduct       *codeOfConductRaw `json:"code_of_conduct"`
		CodeOfConductFile   *healthFileRaw    `json:"code_of_conduct_file"`
		License             *licenseRaw       `json:"license"`
		Contributing        *healthFileRaw    `json:"contributing"`
		Readme              *healthFileRaw    `json:"readme"`
		IssueTemplate       *healthFileRaw    `json:"issue_template"`
		PullRequestTemplate *healthFileRaw    `json:"pull_request_template"`
	} `json:"files"`
	UpdatedAt             *string `json:"updated_at"`
	ContentReportsEnabled *bool   `json:"content_reports_enabled"`
}

// RESTSimpleCodeOfConduct is the code of conduct of the community profile, without its text.
type RESTSimpleCodeOfConduct struct {
	Key   *string `json:"key,omitempty"`
	Name  *string `json:"name,omitempty"`
	URL   *string `json:"url,omitempty"`
	Links Links   `json:"links,omitempty"`
}

// CommunityFiles are the community files of a repository by role; a role without a file is absent.
type CommunityFiles struct {
	CodeOfConduct       *RESTSimpleCodeOfConduct `json:"code_of_conduct,omitempty"`
	CodeOfConductFile   *RESTHealthFile          `json:"code_of_conduct_file,omitempty"`
	License             *RESTLicense             `json:"license,omitempty"`
	Contributing        *RESTHealthFile          `json:"contributing,omitempty"`
	Readme              *RESTHealthFile          `json:"readme,omitempty"`
	IssueTemplate       *RESTHealthFile          `json:"issue_template,omitempty"`
	PullRequestTemplate *RESTHealthFile          `json:"pull_request_template,omitempty"`
}

// CommunityProfile is the community profile of a repository.
type CommunityProfile struct {
	HealthPercentage      int64          `json:"health_percentage"`
	Description           *string        `json:"description,omitempty"`
	Documentation         *string        `json:"documentation,omitempty"`
	Files                 CommunityFiles `json:"files"`
	UpdatedAt             *string        `json:"updated_at,omitempty"`
	ContentReportsEnabled *bool          `json:"content_reports_enabled,omitempty"`
}

func (c *Client) getCommunityProfile(ctx context.Context) (*CommunityProfile, error) {
	const op = "get community profile"
	var raw communityProfileRaw
	if err := c.rest(ctx, op, c.repoPath("community/profile"), &raw); err != nil {
		return nil, actionsFailure(err, communityProfileReadPermission)
	}
	if raw.HealthPercentage == nil {
		return nil, invalidEntry(op, "a community profile")
	}
	result := &CommunityProfile{HealthPercentage: *raw.HealthPercentage, Description: longText(raw.Description),
		Documentation: address(raw.Documentation), UpdatedAt: nameText(raw.UpdatedAt),
		ContentReportsEnabled: raw.ContentReportsEnabled}
	if f := raw.Files; f != nil {
		if conduct := f.CodeOfConduct; conduct != nil {
			result.Files.CodeOfConduct = &RESTSimpleCodeOfConduct{Key: nameText(conduct.Key),
				Name: nameText(conduct.Name), URL: address(conduct.HTMLURL), Links: conduct.links}
		}
		if l := f.License; l != nil {
			result.Files.License = &RESTLicense{Key: nameText(l.Key), Name: nameText(l.Name),
				SPDXID: nameText(l.SPDXID), NodeID: nameText(l.NodeID), URL: address(l.HTMLURL), Links: l.links}
		}
		result.Files.CodeOfConductFile = f.CodeOfConductFile.project()
		result.Files.Contributing = f.Contributing.project()
		result.Files.Readme = f.Readme.project()
		result.Files.IssueTemplate = f.IssueTemplate.project()
		result.Files.PullRequestTemplate = f.PullRequestTemplate.project()
	}
	return result, nil
}

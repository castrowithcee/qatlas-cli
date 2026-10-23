package github

import (
	"bytes"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The gist width: the members of GitHub's gist-simple and base-gist beyond the compact gist. The flat owner
// stays and the account sits next to it. Forks and history are deprecated in a single gist and are read only
// where a list carries them, at most gistListedDetails each. The file contents are not loaded beyond what the
// compact gist already shows. A nested gist reports its own members only.

const gistListedDetails = 10

var gistLinkKeys = []string{"url", "forks_url", "commits_url", "comments_url", "git_pull_url", "git_push_url"}
var gistFileLinkKeys = []string{"raw_url"}
var gistEntryLinkKeys = []string{"url"}

type gistForkRaw struct {
	ID        *string     `json:"id"`
	URL       *string     `json:"url"`
	User      *accountRaw `json:"user"`
	CreatedAt *string     `json:"created_at"`
	UpdatedAt *string     `json:"updated_at"`
}

type gistHistoryRaw struct {
	Version      *string     `json:"version"`
	URL          *string     `json:"url"`
	User         *accountRaw `json:"user"`
	CommittedAt  *string     `json:"committed_at"`
	ChangeStatus *struct {
		Total     *int64 `json:"total"`
		Additions *int64 `json:"additions"`
		Deletions *int64 `json:"deletions"`
	} `json:"change_status"`
}

// GistFileDetail is a file of a gist with its encoding and raw address.
type GistFileDetail struct {
	GistFile
	Encoding *string `json:"encoding,omitempty"`
	Links    Links   `json:"links,omitempty"`
}

// GistOrigin is the gist another gist was forked from.
type GistOrigin struct {
	ID                   *string      `json:"id,omitempty"`
	NodeID               *string      `json:"node_id,omitempty"`
	Description          *string      `json:"description,omitempty"`
	DescriptionTruncated bool         `json:"description_truncated,omitempty"`
	Public               *bool        `json:"public,omitempty"`
	CreatedAt            *string      `json:"created_at,omitempty"`
	UpdatedAt            *string      `json:"updated_at,omitempty"`
	URL                  *string      `json:"url,omitempty"`
	Comments             *int64       `json:"comments,omitempty"`
	CommentsEnabled      *bool        `json:"comments_enabled,omitempty"`
	User                 *RESTAccount `json:"user,omitempty"`
	Owner                *RESTAccount `json:"owner,omitempty"`
	Truncated            *bool        `json:"truncated,omitempty"`
	Links                Links        `json:"links,omitempty"`
}

// GistFork is one fork of a gist.
type GistFork struct {
	ID        *string      `json:"id,omitempty"`
	User      *RESTAccount `json:"user,omitempty"`
	CreatedAt *string      `json:"created_at,omitempty"`
	UpdatedAt *string      `json:"updated_at,omitempty"`
	Links     Links        `json:"links,omitempty"`
}

// GistChangeStatus counts the lines one revision changed.
type GistChangeStatus struct {
	Total     *int64 `json:"total,omitempty"`
	Additions *int64 `json:"additions,omitempty"`
	Deletions *int64 `json:"deletions,omitempty"`
}

// GistRevision is one entry of the history of a gist.
type GistRevision struct {
	Version      *string           `json:"version,omitempty"`
	User         *RESTAccount      `json:"user,omitempty"`
	CommittedAt  *string           `json:"committed_at,omitempty"`
	ChangeStatus *GistChangeStatus `json:"change_status,omitempty"`
	Links        Links             `json:"links,omitempty"`
}

// GistDetail is the view of a gist the two read tools answer: the compact gist and the width of GitHub's
// answer. Its files carry the encoding and the raw address.
type GistDetail struct {
	Gist
	Files            []GistFileDetail `json:"files"`
	NodeID           *string          `json:"node_id,omitempty"`
	Comments         *int64           `json:"comments,omitempty"`
	CommentsEnabled  *bool            `json:"comments_enabled,omitempty"`
	OwnerDetail      *RESTAccount     `json:"owner_detail,omitempty"`
	User             *RESTAccount     `json:"user,omitempty"`
	Truncated        *bool            `json:"truncated,omitempty"`
	ForkOf           *GistOrigin      `json:"fork_of,omitempty"`
	Links            Links            `json:"links,omitempty"`
	Forks            []GistFork       `json:"forks,omitempty"`
	ForksTruncated   bool             `json:"forks_truncated,omitempty"`
	History          []GistRevision   `json:"history,omitempty"`
	HistoryTruncated bool             `json:"history_truncated,omitempty"`
}

// gistText returns a text for the output, or nil when it is empty.
func gistText(s string) *string {
	if s == "" {
		return nil
	}
	return nameText(&s)
}

// gistAccount reads a user member that is an account object. GitHub documents the member of a single gist as a
// text in one place and as an account in another; only an account object is a user.
func gistAccount(raw json.RawMessage) *RESTAccount {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var account accountRaw
	if json.Unmarshal(raw, &account) != nil {
		return nil
	}
	return account.project()
}

// origin projects a nested gist with its own members only.
func (g *gistJSON) origin() *GistOrigin {
	if g == nil {
		return nil
	}
	o := &GistOrigin{ID: gistText(g.ID), NodeID: gistName(g.NodeID), Public: &g.Public, CreatedAt: gistText(g.CreatedAt),
		UpdatedAt: gistText(g.UpdatedAt), URL: gistAddress(g.HTMLURL), Comments: g.Comments,
		CommentsEnabled: g.CommentsEnabled, User: gistAccount(g.User), Owner: g.Owner.project(),
		Truncated: g.Truncated, Links: g.links}
	if g.Description != "" {
		text, cut := cleanText(&g.Description, gistDescriptionLimit, false)
		o.Description, o.DescriptionTruncated = text, cut
	}
	return o
}

func gistName(p *string) *string { return shortText(p) }

func gistAddress(s string) *string {
	if s == "" {
		return nil
	}
	return address(&s)
}

// detail builds the read view. Content follows view; lists adds the capped forks and history a list carries.
func (g gistJSON) detail(op string, content, lists bool) (GistDetail, error) {
	base, err := g.view(op, content)
	if err != nil {
		return GistDetail{}, err
	}
	d := GistDetail{Gist: base, NodeID: gistName(g.NodeID), Comments: g.Comments,
		CommentsEnabled: g.CommentsEnabled, OwnerDetail: g.Owner.project(), User: gistAccount(g.User),
		Truncated: g.Truncated, ForkOf: g.ForkOf.origin(), Links: g.links}
	d.Files = make([]GistFileDetail, 0, len(base.Files))
	for _, file := range base.Files {
		raw := g.Files[file.Filename]
		d.Files = append(d.Files, GistFileDetail{GistFile: file, Encoding: gistText(raw.Encoding),
			Links: gqlLinks(gistFileLinkKeys, raw.RawURL)})
	}
	if !lists {
		return d, nil
	}
	forks := g.Forks
	if len(forks) > gistListedDetails {
		forks, d.ForksTruncated = forks[:gistListedDetails], true
	}
	for _, f := range forks {
		d.Forks = append(d.Forks, GistFork{ID: shortText(f.ID), User: f.User.project(), CreatedAt: shortText(f.CreatedAt),
			UpdatedAt: shortText(f.UpdatedAt), Links: gqlLinks(gistEntryLinkKeys, f.URL)})
	}
	history := g.History
	if len(history) > gistListedDetails {
		history, d.HistoryTruncated = history[:gistListedDetails], true
	}
	for _, h := range history {
		r := GistRevision{Version: shortText(h.Version), User: h.User.project(), CommittedAt: shortText(h.CommittedAt),
			Links: gqlLinks(gistEntryLinkKeys, h.URL)}
		if c := h.ChangeStatus; c != nil {
			r.ChangeStatus = &GistChangeStatus{Total: c.Total, Additions: c.Additions, Deletions: c.Deletions}
		}
		d.History = append(d.History, r)
	}
	return d, nil
}

const gistFileExtraProperties = `"encoding":{"type":"string"},"links":` + `{"type":"object","properties":` +
	`{"raw_url":{"type":"string"}},"additionalProperties":false}`

const gistListFileProperties = `"filename":{"type":"string"},"type":{"type":"string"},"language":{"type":"string"},` +
	`"size":{"type":"integer"},` + gistFileExtraProperties

const gistReadFileProperties = `"filename":{"type":"string"},"type":{"type":"string"},"language":{"type":"string"},` +
	`"size":{"type":"integer"},"binary":{"type":"boolean"},"content":{"type":"string"},` +
	`"content_truncated":{"type":"boolean"},` + gistFileExtraProperties

var gistLinksSchema = linksSchema(gistLinkKeys)

var gistOriginSchema = objectSchema(`"id":{"type":"string"},"node_id":{"type":"string"},` +
	`"description":{"type":"string"},"description_truncated":{"type":"boolean"},"public":{"type":"boolean"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},"url":{"type":"string"},` +
	`"comments":{"type":"integer"},"comments_enabled":{"type":"boolean"},"user":` + restAccountSchema + `,` +
	`"owner":` + restAccountSchema + `,"truncated":{"type":"boolean"},"links":` + gistLinksSchema)

var gistForkSchema = objectSchema(`"id":{"type":"string"},"user":` + restAccountSchema + `,` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},"links":` + linksSchema(gistEntryLinkKeys))

var gistRevisionSchema = objectSchema(`"version":{"type":"string"},"user":` + restAccountSchema + `,` +
	`"committed_at":{"type":"string"},"change_status":` + objectSchema(`"total":{"type":"integer"},`+
	`"additions":{"type":"integer"},"deletions":{"type":"integer"}`) + `,"links":` + linksSchema(gistEntryLinkKeys))

var gistWideProperties = `"node_id":{"type":"string"},"comments":{"type":"integer"},` +
	`"comments_enabled":{"type":"boolean"},` + detailProperty("owner", restAccountSchema, false) + `,` +
	`"user":` + restAccountSchema + `,"truncated":{"type":"boolean"},"fork_of":` + gistOriginSchema + `,` +
	`"links":` + gistLinksSchema

var gistListProperties = gistHeadProperties + `,"files":{"type":"array","items":{"type":"object","properties":{` +
	gistListFileProperties + `},"required":["filename","size"],"additionalProperties":false}},` +
	gistWideProperties + `,"forks":{"type":"array","items":` + gistForkSchema + `},"forks_truncated":{"type":"boolean"},` +
	`"history":{"type":"array","items":` + gistRevisionSchema + `},"history_truncated":{"type":"boolean"}`

var gistReadProperties = gistHeadProperties + `,"files":{"type":"array","items":{"type":"object","properties":{` +
	gistReadFileProperties + `},"required":["filename","size"],"additionalProperties":false}},` + gistWideProperties

// gistWideFields describes the members of a gist the compact gist lacks.
var gistWideFields = []capability.Field{
	{Name: "node_id", Description: "GraphQL node id of the gist"},
	{Name: "comments", Description: "Number of comments on the gist"},
	{Name: "comments_enabled", Description: "True when the gist accepts comments"},
	detailField("owner", "Account", false),
	{Name: "user", Description: "Account of the gist as an account object, absent when GitHub sends none: " +
		restAccountMembers},
	{Name: "truncated", Description: "True when GitHub cut the file list of the gist"},
	{Name: "fork_of", Description: "Gist this gist was forked from: id, node_id, description (untrusted data, " +
		"cut at 500 characters), public, times, url (the web address), comments, comments_enabled, user and " +
		"owner as accounts, truncated, and links (the REST url and the other addresses); absent for an original"},
	{Name: "links", Description: "REST url, forks_url, commits_url, comments_url, git_pull_url, and git_push_url " +
		"of the gist; untrusted data, never fetched; with the id and the url they make a secret gist readable " +
		"without a login"},
}

// gistListWideFields describes the members only a list carries.
var gistListWideFields = []capability.Field{
	{Name: "forks", Description: "At most 10 forks of the gist, a list only: id, user, times, and links (url); " +
		"a single gist does not carry them, since GitHub deprecated them there"},
	{Name: "forks_truncated", Description: "True when the gist has more forks than listed"},
	{Name: "history", Description: "At most 10 revisions of the gist, a list only: version, user, committed_at, " +
		"change_status (total, additions, deletions), and links (url); a single gist does not carry them, since " +
		"GitHub deprecated them there"},
	{Name: "history_truncated", Description: "True when the gist has more revisions than listed"},
}

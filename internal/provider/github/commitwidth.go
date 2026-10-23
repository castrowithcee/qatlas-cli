package github

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The commit width: the members of GitHub's REST commit beyond the compact views of the commit tools. The
// flat members keep their own treatment in the tools; everything here passes through the commit projection.
// Stats and files stay with github.commits.get's own members, which cover them completely.

// maxCommitterLength bounds the committer filter of github.commits.list, a login or an email address.
const maxCommitterLength = 256

// committerPattern admits a login or an email address; validCommitter applies the real check.
const committerPattern = `^([A-Za-z0-9][A-Za-z0-9_-]{0,99}(\\[bot\\])?|[^\\s@]+@[^\\s@]+)$`

// validCommitter accepts a login, with the suffix of a bot account, or an email address of at most
// maxCommitterLength characters without whitespace or control characters.
func validCommitter(value string) bool {
	if value == "" || utf8.RuneCountInString(value) > maxCommitterLength || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	if local, domain, ok := strings.Cut(value, "@"); ok {
		return local != "" && domain != "" && !strings.Contains(domain, "@")
	}
	return validLogin(strings.TrimSuffix(value, "[bot]"))
}

// commitItem decodes one REST commit twice from the same object: the compact view and the width. A member
// the width cannot read is left out rather than failing a read the compact view already decoded.
type commitItem struct {
	commit commitJSON
	wide   commitRaw
}

func (e *commitItem) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &e.commit); err != nil {
		return err
	}
	_ = json.Unmarshal(data, &e.wide)
	return nil
}

// CommitWidth holds the members of GitHub's commit that every commit read adds to its compact view.
type CommitWidth struct {
	NodeID          *string            `json:"node_id,omitempty"`
	URL             *string            `json:"url,omitempty"`
	Links           Links              `json:"links,omitempty"`
	AuthorDetail    *RESTAccount       `json:"author_detail,omitempty"`
	CommitterDetail *RESTAccount       `json:"committer_detail,omitempty"`
	Commit          *RESTCommitInner   `json:"commit,omitempty"`
	ParentsDetails  []RESTCommitParent `json:"parents_details,omitempty"`
}

// width projects the width of one commit without its stats and files.
func (r *commitRaw) width() CommitWidth {
	lean := *r
	lean.Stats, lean.Files = nil, nil
	p := lean.project()
	return CommitWidth{NodeID: p.NodeID, URL: p.URL, Links: p.Links, AuthorDetail: p.AuthorDetail,
		CommitterDetail: p.CommitterDetail, Commit: p.Commit, ParentsDetails: p.ParentsDetails}
}

func (e *commitItem) entry() CommitEntry {
	entry := e.commit.entry()
	entry.CommitWidth = e.wide.width()
	return entry
}

func (e *commitItem) detail() CommitDetail {
	detail := e.commit.detail()
	detail.CommitWidth = e.wide.width()
	return detail
}

// commitSearchItem decodes one commit search match: the compact view, the width, and the search members.
type commitSearchItem struct {
	compact struct {
		SHA        string `json:"sha"`
		HTMLURL    string `json:"html_url"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Commit struct {
			Message string `json:"message"`
			Author  struct {
				Name string `json:"name"`
				Date string `json:"date"`
			} `json:"author"`
		} `json:"commit"`
	}
	wide commitRaw
	more struct {
		Repository  *repositoryRaw `json:"repository"`
		Score       *json.Number   `json:"score"`
		TextMatches []textMatchRaw `json:"text_matches"`
	}
}

func (e *commitSearchItem) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &e.compact); err != nil {
		return err
	}
	_ = json.Unmarshal(data, &e.wide)
	_ = json.Unmarshal(data, &e.more)
	return nil
}

// Schemas and fields of the commit width.

var commitWidthProperties = `"node_id":{"type":"string"},"url":{"type":"string"},"links":` +
	linksSchema(restCommitLinkKeys) + `,"author_detail":` + restAccountSchema + `,"committer_detail":` +
	restAccountSchema + `,"commit":` + restCommitInnerSchema + `,"parents_details":` + restCommitParentsSchema

var commitWidthFields = withoutField(withoutField(withoutField(restCommitFields, "stats"), "files"),
	"files_truncated")

var commitSearchKeys = `"sort":{"type":"string","enum":["author-date","committer-date"]},` +
	`"order":{"type":"string","enum":["asc","desc"]},"text_matches":{"type":"boolean"}`

var commitSearchSortValues = []string{"author-date", "committer-date"}

var commitSearchArguments = append([]capability.Argument{searchTermsArgument,
	{Name: "sort", Description: "author-date or committer-date; GitHub's best match order when omitted"},
	{Name: "order", Description: "asc or desc; applies with sort, desc when omitted"},
	{Name: "text_matches", Description: "True adds text_matches, the places the terms matched; false when omitted"},
}, pagingArguments...)

var commitSearchExtraProperties = `"committer":{"type":"string"},"parents":{"type":"integer"},` +
	`"repository_detail":` + restRepositorySchema + `,"score":{"type":"number"},` +
	`"text_matches":{"type":"array","maxItems":100,"items":` + textMatchSchema + `},` +
	`"text_matches_truncated":{"type":"boolean"}`

var commitSearchExtraFields = []capability.Field{
	{Name: "committer", Description: "Login of the committer account when GitHub reports one"},
	{Name: "parents", Description: "Number of parents of the commit"},
	{Name: "repository_detail", Description: "Repository of the commit with the members of GitHub's " +
		"repository, untrusted data, addresses never fetched; a repository outside the connection's targets " +
		"keeps only id, node_id, and full_name"},
	{Name: "score", Description: "Relevance score GitHub gave the match"},
	{Name: "text_matches", Description: "Where the terms matched, only when text_matches is set, at most 100: " +
		"object_type, property, fragment (cut at 1000 characters, fragment_truncated says so), matches (text " +
		"and indices), and links with the object_url; untrusted data, never fetched"},
	{Name: "text_matches_truncated", Description: "True when more text matches followed than shown"},
}

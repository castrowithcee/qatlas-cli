package github

import (
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The commit projection covers the REST commit object beyond the flat members each commit tool reports on
// its own: node id, web address, links, the account objects, the git commit with its identities, tree and
// verification, the parents, the stats, and the changed files. A tool keeps its flat members (author as a
// login, parents as a count or list, message) and embeds RESTCommit next to them.

// Bounds of the commit projection.
const (
	projectionSignatureLimit = 4096 // runes kept of a verification payload or signature
	projectionPathLimit      = 4096 // runes kept of a file path
)

var (
	restCommitLinkKeys      = []string{"url", "comments_url"}
	restCommitInnerLinkKeys = []string{"url"}
	restCommitTreeLinkKeys  = []string{"url"}
	restCommitParentKeys    = []string{"url"}
	restDiffEntryLinkKeys   = []string{"blob_url", "raw_url", "contents_url"}
)

type commitIdentityRaw struct {
	Name  *string `json:"name"`
	Email *string `json:"email"`
	Date  *string `json:"date"`
}

type commitVerificationRaw struct {
	Verified   *bool   `json:"verified"`
	Reason     *string `json:"reason"`
	Payload    *string `json:"payload"`
	Signature  *string `json:"signature"`
	VerifiedAt *string `json:"verified_at"`
}

type commitTreeRaw struct {
	SHA   *string `json:"sha"`
	links Links
}

func (r *commitTreeRaw) UnmarshalJSON(data []byte) (err error) {
	type plain commitTreeRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, restCommitTreeLinkKeys)
	return err
}

type commitInnerRaw struct {
	Author       *commitIdentityRaw     `json:"author"`
	Committer    *commitIdentityRaw     `json:"committer"`
	Message      *string                `json:"message"`
	CommentCount *int                   `json:"comment_count"`
	Tree         *commitTreeRaw         `json:"tree"`
	Verification *commitVerificationRaw `json:"verification"`
	links        Links
}

func (r *commitInnerRaw) UnmarshalJSON(data []byte) (err error) {
	type plain commitInnerRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, restCommitInnerLinkKeys)
	return err
}

type commitParentRaw struct {
	SHA     *string `json:"sha"`
	HTMLURL *string `json:"html_url"`
	links   Links
}

func (r *commitParentRaw) UnmarshalJSON(data []byte) (err error) {
	type plain commitParentRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, restCommitParentKeys)
	return err
}

type commitStatsRaw struct {
	Additions *int `json:"additions"`
	Deletions *int `json:"deletions"`
	Total     *int `json:"total"`
}

// commitFileRaw is the REST diff-entry.
type commitFileRaw struct {
	SHA              *string `json:"sha"`
	Filename         *string `json:"filename"`
	Status           *string `json:"status"`
	Additions        *int    `json:"additions"`
	Deletions        *int    `json:"deletions"`
	Changes          *int    `json:"changes"`
	Patch            *string `json:"patch"`
	PreviousFilename *string `json:"previous_filename"`
	links            Links
}

func (r *commitFileRaw) UnmarshalJSON(data []byte) (err error) {
	type plain commitFileRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, restDiffEntryLinkKeys)
	return err
}

// commitRaw is the REST commit object.
type commitRaw struct {
	SHA       *string           `json:"sha"`
	NodeID    *string           `json:"node_id"`
	HTMLURL   *string           `json:"html_url"`
	Commit    *commitInnerRaw   `json:"commit"`
	Author    *accountRaw       `json:"author"`
	Committer *accountRaw       `json:"committer"`
	Parents   []commitParentRaw `json:"parents"`
	Stats     *commitStatsRaw   `json:"stats"`
	Files     []commitFileRaw   `json:"files"`
	links     Links
}

func (r *commitRaw) UnmarshalJSON(data []byte) (err error) {
	type plain commitRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, restCommitLinkKeys)
	return err
}

// RESTCommitIdentity is the author or committer of the git commit.
type RESTCommitIdentity struct {
	Name  *string `json:"name,omitempty"`
	Email *string `json:"email,omitempty"`
	Date  *string `json:"date,omitempty"`
}

// RESTCommitVerification is the signature verification of a commit; payload and signature are cut.
type RESTCommitVerification struct {
	Verified           *bool   `json:"verified,omitempty"`
	Reason             *string `json:"reason,omitempty"`
	VerifiedAt         *string `json:"verified_at,omitempty"`
	Payload            *string `json:"payload,omitempty"`
	PayloadTruncated   bool    `json:"payload_truncated,omitempty"`
	Signature          *string `json:"signature,omitempty"`
	SignatureTruncated bool    `json:"signature_truncated,omitempty"`
}

// RESTCommitTree is the tree a commit points to.
type RESTCommitTree struct {
	SHA   *string `json:"sha,omitempty"`
	Links Links   `json:"links,omitempty"`
}

// RESTCommitInner is the git commit behind a REST commit.
type RESTCommitInner struct {
	Message          *string                 `json:"message,omitempty"`
	MessageTruncated bool                    `json:"message_truncated,omitempty"`
	CommentCount     *int                    `json:"comment_count,omitempty"`
	Author           *RESTCommitIdentity     `json:"author,omitempty"`
	Committer        *RESTCommitIdentity     `json:"committer,omitempty"`
	Tree             *RESTCommitTree         `json:"tree,omitempty"`
	Verification     *RESTCommitVerification `json:"verification,omitempty"`
	Links            Links                   `json:"links,omitempty"`
}

// RESTCommitParent is one parent of a commit.
type RESTCommitParent struct {
	SHA   *string `json:"sha,omitempty"`
	URL   *string `json:"url,omitempty"`
	Links Links   `json:"links,omitempty"`
}

// RESTCommitStats is the line count of a commit.
type RESTCommitStats struct {
	Additions *int `json:"additions,omitempty"`
	Deletions *int `json:"deletions,omitempty"`
	Total     *int `json:"total,omitempty"`
}

// RESTCommitFile is one changed file of a commit. Patch is untrusted data, cut to maxPatchBytes.
type RESTCommitFile struct {
	SHA              *string `json:"sha,omitempty"`
	Path             *string `json:"path,omitempty"`
	Status           *string `json:"status,omitempty"`
	Additions        *int    `json:"additions,omitempty"`
	Deletions        *int    `json:"deletions,omitempty"`
	Changes          *int    `json:"changes,omitempty"`
	PreviousFilename *string `json:"previous_filename,omitempty"`
	Patch            string  `json:"patch,omitempty"`
	PatchTruncated   bool    `json:"patch_truncated,omitempty"`
	Links            Links   `json:"links,omitempty"`
}

// RESTCommit holds the members of a REST commit that a tool reports next to its own flat members.
type RESTCommit struct {
	SHA             string             `json:"sha"`
	NodeID          *string            `json:"node_id,omitempty"`
	URL             *string            `json:"url,omitempty"`
	Links           Links              `json:"links,omitempty"`
	AuthorDetail    *RESTAccount       `json:"author_detail,omitempty"`
	CommitterDetail *RESTAccount       `json:"committer_detail,omitempty"`
	Commit          *RESTCommitInner   `json:"commit,omitempty"`
	ParentsDetails  []RESTCommitParent `json:"parents_details,omitempty"`
	Stats           *RESTCommitStats   `json:"stats,omitempty"`
	Files           []RESTCommitFile   `json:"files,omitempty"`
	FilesTruncated  bool               `json:"files_truncated,omitempty"`
}

// commitAccount projects the author or committer account; GitHub sends null or an empty object for a commit
// without a linked account.
func commitAccount(r *accountRaw) *RESTAccount {
	if r == nil || r.Login == nil && r.ID == nil {
		return nil
	}
	return r.project()
}

func (r *commitIdentityRaw) project() *RESTCommitIdentity {
	if r == nil || r.Name == nil && r.Email == nil && r.Date == nil {
		return nil
	}
	return &RESTCommitIdentity{Name: nameText(r.Name), Email: nameText(r.Email), Date: nameText(r.Date)}
}

func (r *commitVerificationRaw) project() *RESTCommitVerification {
	if r == nil {
		return nil
	}
	payload, payloadCut := cleanText(r.Payload, projectionSignatureLimit, true)
	signature, signatureCut := cleanText(r.Signature, projectionSignatureLimit, true)
	return &RESTCommitVerification{Verified: r.Verified, Reason: nameText(r.Reason), VerifiedAt: nameText(r.VerifiedAt),
		Payload: payload, PayloadTruncated: payloadCut, Signature: signature, SignatureTruncated: signatureCut}
}

// message returns the commit message bounded like every full commit message of the commit tools.
func (r *commitRaw) message() (string, bool) {
	if r.Commit == nil || r.Commit.Message == nil {
		return "", false
	}
	text, cut := cleanText(r.Commit.Message, maxCommitMessageLength, true)
	return *text, cut
}

// authorLogin and committerLogin are the flat names of a commit: the login of the linked account, the git
// identity's name otherwise.
func (r *commitRaw) authorLogin() string {
	if r.Author != nil && r.Author.Login != nil && *r.Author.Login != "" {
		return valueOr(nameText(r.Author.Login))
	}
	if r.Commit != nil && r.Commit.Author != nil {
		return valueOr(nameText(r.Commit.Author.Name))
	}
	return ""
}

func (r *commitRaw) committerLogin() string {
	if r.Committer != nil && r.Committer.Login != nil && *r.Committer.Login != "" {
		return valueOr(nameText(r.Committer.Login))
	}
	if r.Commit != nil && r.Commit.Committer != nil {
		return valueOr(nameText(r.Commit.Committer.Name))
	}
	return ""
}

// authoredAt is the date of the git author identity.
func (r *commitRaw) authoredAt() string {
	if r.Commit == nil || r.Commit.Author == nil {
		return ""
	}
	return valueOr(nameText(r.Commit.Author.Date))
}

func (r *commitRaw) project() RESTCommit {
	commit := RESTCommit{SHA: valueOr(nameText(r.SHA)), NodeID: nameText(r.NodeID), URL: address(r.HTMLURL),
		Links: r.links, AuthorDetail: commitAccount(r.Author), CommitterDetail: commitAccount(r.Committer)}
	if c := r.Commit; c != nil {
		inner := &RESTCommitInner{CommentCount: c.CommentCount, Author: c.Author.project(),
			Committer: c.Committer.project(), Verification: c.Verification.project(), Links: c.links}
		if c.Message != nil {
			message, cut := r.message()
			inner.Message, inner.MessageTruncated = &message, cut
		}
		if t := c.Tree; t != nil && (t.SHA != nil || t.links != nil) {
			inner.Tree = &RESTCommitTree{SHA: nameText(t.SHA), Links: t.links}
		}
		commit.Commit = inner
	}
	parents, _ := capped(r.Parents)
	for _, parent := range parents {
		commit.ParentsDetails = append(commit.ParentsDetails,
			RESTCommitParent{SHA: nameText(parent.SHA), URL: address(parent.HTMLURL), Links: parent.links})
	}
	if s := r.Stats; s != nil {
		commit.Stats = &RESTCommitStats{Additions: s.Additions, Deletions: s.Deletions, Total: s.Total}
	}
	files := r.Files
	if len(files) >= maxCommitFiles {
		files, commit.FilesTruncated = files[:maxCommitFiles], true
	}
	for _, file := range files {
		entry := RESTCommitFile{SHA: nameText(file.SHA), Additions: file.Additions, Deletions: file.Deletions,
			Changes: file.Changes, Status: nameText(file.Status), Links: file.links}
		entry.Path, _ = cleanText(file.Filename, projectionPathLimit, false)
		entry.PreviousFilename, _ = cleanText(file.PreviousFilename, projectionPathLimit, false)
		if file.Patch != nil {
			entry.Patch, entry.PatchTruncated = truncatePatch(*file.Patch)
		}
		commit.Files = append(commit.Files, entry)
	}
	return commit
}

var restCommitIdentitySchema = objectSchema(`"name":{"type":"string"},"email":{"type":"string"},` +
	`"date":{"type":"string"}`)

var restCommitInnerSchema = objectSchema(`"message":{"type":"string"},"message_truncated":{"type":"boolean"},` +
	`"comment_count":{"type":"integer"},"author":` + restCommitIdentitySchema + `,"committer":` +
	restCommitIdentitySchema + `,"tree":` + objectSchema(`"sha":{"type":"string"},"links":`+
	linksSchema(restCommitTreeLinkKeys)) + `,"verification":` + objectSchema(`"verified":{"type":"boolean"},`+
	`"reason":{"type":"string"},"verified_at":{"type":"string"},"payload":{"type":"string"},`+
	`"payload_truncated":{"type":"boolean"},"signature":{"type":"string"},"signature_truncated":{"type":"boolean"}`) +
	`,"links":` + linksSchema(restCommitInnerLinkKeys))

var restCommitFileSchema = objectSchema(`"sha":{"type":"string"},"path":{"type":"string"},` +
	`"status":{"type":"string"},"additions":{"type":"integer"},"deletions":{"type":"integer"},` +
	`"changes":{"type":"integer"},"previous_filename":{"type":"string"},"patch":{"type":"string"},` +
	`"patch_truncated":{"type":"boolean"},"links":` + linksSchema(restDiffEntryLinkKeys))

// restCommitProperties describes RESTCommit.
var restCommitProperties = `"sha":{"type":"string"},"node_id":{"type":"string"},"url":{"type":"string"},` +
	`"links":` + linksSchema(restCommitLinkKeys) + `,"author_detail":` + restAccountSchema +
	`,"committer_detail":` + restAccountSchema + `,"commit":` + restCommitInnerSchema +
	`,"parents_details":{"type":"array","maxItems":100,"items":` + objectSchema(`"sha":{"type":"string"},`+
	`"url":{"type":"string"},"links":`+linksSchema(restCommitParentKeys)) + `},"stats":` +
	objectSchema(`"additions":{"type":"integer"},"deletions":{"type":"integer"},"total":{"type":"integer"}`) +
	`,"files":{"type":"array","maxItems":300,"items":` + restCommitFileSchema + `},"files_truncated":{"type":"boolean"}`

// restCommitFields describe the members of RESTCommit that a tool does not report on its own.
var restCommitFields = []capability.Field{
	{Name: "node_id", Description: "GraphQL node id of the commit"},
	{Name: "url", Description: "Web address of the commit, untrusted data, never fetched"},
	{Name: "links", Description: "REST url and comments_url of the commit by their GitHub names, untrusted data, never fetched"},
	{Name: "author_detail", Description: "Account behind author: " + restAccountMembers +
		"; absent for a commit without a linked account"},
	{Name: "committer_detail", Description: "Account of the committer: " + restAccountMembers +
		"; absent for a commit without a linked account"},
	{Name: "commit", Description: "Git commit: message (cut at 4096 characters, message_truncated says so), " +
		"comment_count, author and committer (name, email, date), tree (sha and links), verification " +
		"(verified, reason, verified_at, payload and signature each cut at 4096 characters with " +
		"payload_truncated and signature_truncated), and links; untrusted data"},
	{Name: "parents_details", Description: "Parents, at most 100, each with sha, url (the web address), and links"},
	{Name: "stats", Description: "additions, deletions, and total lines of the commit, when GitHub sends them"},
	{Name: "files", Description: "Changed files, at most 300, with sha, path, status, additions, deletions, " +
		"changes, previous_filename, patch cut to at most 4096 bytes with patch_truncated, and links; patch is " +
		"untrusted data"},
	{Name: "files_truncated", Description: "True when the commit held at least as many files as were returned"},
}

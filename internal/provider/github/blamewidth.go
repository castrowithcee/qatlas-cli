package github

import (
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The blame width: the members of GitHub's BlameRange and of the Commit of each range beyond the compact
// range. The repository of the commit is left out, since the call names it; message repeats the headline
// and the body; the deprecated changedFiles and pushedDate stay out; the connections of a commit (parents,
// authors, history, check suites, comments, deployments, associated pull requests, submodules) are not read,
// since a nested object reports its own fields only; the contexts of a status and a status check rollup are
// lists of their own.

// Bounds of the text a commit adds. A cut text says so in its own _truncated member.
const (
	blameSignatureLimit = 2048
)

const blameSignatureSelection = `__typename state isValid wasSignedByGitHub email signer{` + gqlUserSelection + `} ` +
	`verifiedAt payload signature ... on GpgSignature{keyId} ... on SshSignature{keyFingerprint}`

// blameCompactCommitSelection is the commit of a range without commit_details; blameCommitSelection adds
// the members that grow the answer.
const blameCompactCommitSelection = `oid abbreviatedOid messageHeadline authoredDate committedDate ` +
	`author{` + gqlGitActorSelection + `} committer{` + gqlGitActorSelection + `} url`

const blameDetailCommitSelection = `id messageBody authoredByCommitter committedViaWeb additions deletions ` +
	`changedFilesIfAvailable onBehalfOf{` + gqlOrganizationSelection + `} signature{` + blameSignatureSelection + `} ` +
	`status{id state} statusCheckRollup{id state} commitUrl resourcePath commitResourcePath treeUrl ` +
	`treeResourcePath tarballUrl zipballUrl tree{id oid abbreviatedOid commitUrl commitResourcePath} ` +
	`viewerCanSubscribe viewerSubscription`

const blameCommitSelection = blameCompactCommitSelection + ` ` + blameDetailCommitSelection

type blameSignatureRaw struct {
	Typename          *string        `json:"__typename"`
	State             *string        `json:"state"`
	IsValid           *bool          `json:"isValid"`
	WasSignedByGitHub *bool          `json:"wasSignedByGitHub"`
	Email             *string        `json:"email"`
	Signer            *gqlAccountRaw `json:"signer"`
	VerifiedAt        *string        `json:"verifiedAt"`
	Payload           *string        `json:"payload"`
	Signature         *string        `json:"signature"`
	KeyID             *string        `json:"keyId"`
	KeyFingerprint    *string        `json:"keyFingerprint"`
}

type blameStateRaw struct {
	ID    *string `json:"id"`
	State *string `json:"state"`
}

type blameTreeRaw struct {
	ID                 *string `json:"id"`
	OID                *string `json:"oid"`
	AbbreviatedOID     *string `json:"abbreviatedOid"`
	CommitURL          *string `json:"commitUrl"`
	CommitResourcePath *string `json:"commitResourcePath"`
}

// blameCommitRaw is the Commit of one blame range as blameCommitSelection asks for it.
type blameCommitRaw struct {
	ID                      *string            `json:"id"`
	OID                     string             `json:"oid"`
	AbbreviatedOID          *string            `json:"abbreviatedOid"`
	MessageHeadline         *string            `json:"messageHeadline"`
	MessageBody             *string            `json:"messageBody"`
	AuthoredDate            *string            `json:"authoredDate"`
	CommittedDate           *string            `json:"committedDate"`
	AuthoredByCommitter     *bool              `json:"authoredByCommitter"`
	CommittedViaWeb         *bool              `json:"committedViaWeb"`
	Additions               *int64             `json:"additions"`
	Deletions               *int64             `json:"deletions"`
	ChangedFilesIfAvailable *int64             `json:"changedFilesIfAvailable"`
	Author                  *gqlGitActorRaw    `json:"author"`
	Committer               *gqlGitActorRaw    `json:"committer"`
	OnBehalfOf              *gqlAccountRaw     `json:"onBehalfOf"`
	Signature               *blameSignatureRaw `json:"signature"`
	Status                  *blameStateRaw     `json:"status"`
	StatusCheckRollup       *blameStateRaw     `json:"statusCheckRollup"`
	URL                     *string            `json:"url"`
	CommitURL               *string            `json:"commitUrl"`
	ResourcePath            *string            `json:"resourcePath"`
	CommitResourcePath      *string            `json:"commitResourcePath"`
	TreeURL                 *string            `json:"treeUrl"`
	TreeResourcePath        *string            `json:"treeResourcePath"`
	TarballURL              *string            `json:"tarballUrl"`
	ZipballURL              *string            `json:"zipballUrl"`
	Tree                    *blameTreeRaw      `json:"tree"`
	ViewerCanSubscribe      *bool              `json:"viewerCanSubscribe"`
	ViewerSubscription      *string            `json:"viewerSubscription"`
}

// BlameSignature is the signature of a commit. Payload and Signature are untrusted and cut.
type BlameSignature struct {
	Type               *string         `json:"type,omitempty"`
	State              *string         `json:"state,omitempty"`
	IsValid            *bool           `json:"is_valid,omitempty"`
	WasSignedByGitHub  *bool           `json:"was_signed_by_github,omitempty"`
	Email              *string         `json:"email,omitempty"`
	Signer             *GraphQLAccount `json:"signer,omitempty"`
	VerifiedAt         *string         `json:"verified_at,omitempty"`
	Payload            *string         `json:"payload,omitempty"`
	PayloadTruncated   bool            `json:"payload_truncated,omitempty"`
	Signature          *string         `json:"signature,omitempty"`
	SignatureTruncated bool            `json:"signature_truncated,omitempty"`
	KeyID              *string         `json:"key_id,omitempty"`
	KeyFingerprint     *string         `json:"key_fingerprint,omitempty"`
}

// BlameState is the state of the status or the status check rollup of a commit.
type BlameState struct {
	ID    *string `json:"id,omitempty"`
	State *string `json:"state,omitempty"`
}

// BlameTree is the tree of a commit.
type BlameTree struct {
	ID             *string `json:"id,omitempty"`
	OID            *string `json:"oid,omitempty"`
	AbbreviatedOID *string `json:"abbreviated_oid,omitempty"`
	Links          Links   `json:"links,omitempty"`
}

// BlameCommit is the commit of one blame range.
type BlameCommit struct {
	ID                      *string          `json:"id,omitempty"`
	OID                     *string          `json:"oid,omitempty"`
	AbbreviatedOID          *string          `json:"abbreviated_oid,omitempty"`
	MessageHeadline         *string          `json:"message_headline,omitempty"`
	MessageBody             *string          `json:"message_body,omitempty"`
	MessageBodyTruncated    bool             `json:"message_body_truncated,omitempty"`
	AuthoredDate            *string          `json:"authored_date,omitempty"`
	CommittedDate           *string          `json:"committed_date,omitempty"`
	AuthoredByCommitter     *bool            `json:"authored_by_committer,omitempty"`
	CommittedViaWeb         *bool            `json:"committed_via_web,omitempty"`
	Additions               *int64           `json:"additions,omitempty"`
	Deletions               *int64           `json:"deletions,omitempty"`
	ChangedFilesIfAvailable *int64           `json:"changed_files_if_available,omitempty"`
	Author                  *GraphQLGitActor `json:"author,omitempty"`
	Committer               *GraphQLGitActor `json:"committer,omitempty"`
	OnBehalfOf              *GraphQLAccount  `json:"on_behalf_of,omitempty"`
	Signature               *BlameSignature  `json:"signature,omitempty"`
	Status                  *BlameState      `json:"status,omitempty"`
	StatusCheckRollup       *BlameState      `json:"status_check_rollup,omitempty"`
	URL                     *string          `json:"url,omitempty"`
	Links                   Links            `json:"links,omitempty"`
	Tree                    *BlameTree       `json:"tree,omitempty"`
	ViewerCanSubscribe      *bool            `json:"viewer_can_subscribe,omitempty"`
	ViewerSubscription      *string          `json:"viewer_subscription,omitempty"`
}

var blameCommitLinkKeys = []string{"commit_url", "resource_path", "commit_resource_path", "tree_url",
	"tree_resource_path", "tarball_url", "zipball_url"}

var blameTreeLinkKeys = []string{"commit_url", "commit_resource_path"}

// archiveAddress keeps an archive address only without a query or user information: for a private
// repository GitHub states that these addresses are temporary, which means they carry a token that grants
// access without a sign-in, and that never leaves the tool.
func archiveAddress(p *string) *string {
	a := address(p)
	if a == nil {
		return nil
	}
	parsed, err := url.Parse(*a)
	if err != nil || parsed.RawQuery != "" || parsed.User != nil || parsed.Fragment != "" {
		return nil
	}
	return a
}

// cutText bounds a multi-line untrusted text and reports whether it was cut; an empty text is absent.
func cutText(p *string, limit int) (*string, bool) {
	if p == nil || *p == "" {
		return nil, false
	}
	return cleanText(p, limit, true)
}

func (r *blameSignatureRaw) project() *BlameSignature {
	if r == nil {
		return nil
	}
	s := &BlameSignature{Type: gqlName(r.Typename), State: gqlName(r.State), IsValid: r.IsValid,
		WasSignedByGitHub: r.WasSignedByGitHub, Email: gqlName(r.Email), Signer: r.Signer.project(),
		VerifiedAt: gqlName(r.VerifiedAt), KeyID: gqlName(r.KeyID), KeyFingerprint: gqlName(r.KeyFingerprint)}
	s.Payload, s.PayloadTruncated = cutText(r.Payload, blameSignatureLimit)
	s.Signature, s.SignatureTruncated = cutText(r.Signature, blameSignatureLimit)
	return s
}

func (r *blameStateRaw) project() *BlameState {
	if r == nil {
		return nil
	}
	return &BlameState{ID: gqlName(r.ID), State: gqlName(r.State)}
}

func (r *blameTreeRaw) project() *BlameTree {
	if r == nil {
		return nil
	}
	return &BlameTree{ID: gqlName(r.ID), OID: gqlName(r.OID), AbbreviatedOID: gqlName(r.AbbreviatedOID),
		Links: gqlLinks(blameTreeLinkKeys, r.CommitURL, r.CommitResourcePath)}
}

func (r *blameCommitRaw) project() *BlameCommit {
	c := &BlameCommit{ID: gqlName(r.ID), OID: gqlName(&r.OID), AbbreviatedOID: gqlName(r.AbbreviatedOID),
		MessageHeadline: gqlText(r.MessageHeadline), AuthoredDate: gqlName(r.AuthoredDate),
		CommittedDate: gqlName(r.CommittedDate), AuthoredByCommitter: r.AuthoredByCommitter,
		CommittedViaWeb: r.CommittedViaWeb, Additions: r.Additions, Deletions: r.Deletions,
		ChangedFilesIfAvailable: r.ChangedFilesIfAvailable, Author: r.Author.project(),
		Committer: r.Committer.project(), OnBehalfOf: r.OnBehalfOf.project(), Signature: r.Signature.project(),
		Status: r.Status.project(), StatusCheckRollup: r.StatusCheckRollup.project(), URL: address(r.URL),
		Tree: r.Tree.project(), ViewerCanSubscribe: r.ViewerCanSubscribe,
		ViewerSubscription: gqlName(r.ViewerSubscription)}
	c.MessageBody, c.MessageBodyTruncated = cutText(r.MessageBody, maxCommitMessageLength)
	c.Links = gqlLinks(blameCommitLinkKeys, r.CommitURL, r.ResourcePath, r.CommitResourcePath, r.TreeURL,
		r.TreeResourcePath, archiveAddress(r.TarballURL), archiveAddress(r.ZipballURL))
	return c
}

var blameSignatureSchema = objectSchema(`"type":{"type":"string"},"state":{"type":"string"},` +
	`"is_valid":{"type":"boolean"},"was_signed_by_github":{"type":"boolean"},"email":{"type":"string"},` +
	`"signer":` + gqlAccountSchema + `,"verified_at":{"type":"string"},"payload":{"type":"string"},` +
	`"payload_truncated":{"type":"boolean"},"signature":{"type":"string"},` +
	`"signature_truncated":{"type":"boolean"},"key_id":{"type":"string"},"key_fingerprint":{"type":"string"}`)

var blameStateSchema = objectSchema(`"id":{"type":"string"},"state":{"type":"string"}`)

var blameTreeSchema = objectSchema(`"id":{"type":"string"},"oid":{"type":"string"},` +
	`"abbreviated_oid":{"type":"string"},"links":` + linksSchema(blameTreeLinkKeys))

var blameCommitSchema = objectSchema(`"id":{"type":"string"},"oid":{"type":"string"},` +
	`"abbreviated_oid":{"type":"string"},"message_headline":{"type":"string"},` +
	`"message_body":{"type":"string"},"message_body_truncated":{"type":"boolean"},` +
	`"authored_date":{"type":"string"},"committed_date":{"type":"string"},` +
	`"authored_by_committer":{"type":"boolean"},"committed_via_web":{"type":"boolean"},` +
	`"additions":{"type":"integer"},"deletions":{"type":"integer"},"changed_files_if_available":{"type":"integer"},` +
	`"author":` + gqlGitActorSchema + `,"committer":` + gqlGitActorSchema + `,"on_behalf_of":` + gqlAccountSchema + `,` +
	`"signature":` + blameSignatureSchema + `,"status":` + blameStateSchema + `,` +
	`"status_check_rollup":` + blameStateSchema + `,"url":{"type":"string"},"links":` +
	linksSchema(blameCommitLinkKeys) + `,"tree":` + blameTreeSchema + `,"viewer_can_subscribe":{"type":"boolean"},` +
	`"viewer_subscription":{"type":"string"}`)

// blameRangeWidthProperties are the schema properties a blame range adds to its compact members.
var blameRangeWidthProperties = `"age":{"type":"integer"},` + detailProperty("author", gqlGitActorSchema, false) +
	`,"commit":` + blameCommitSchema

var blameWidthFields = []capability.Field{
	{Name: "age", Description: "Recency of the range's change from 1 (new) to 10 (old)"},
	detailField("author", "Git actor of the commit", false),
	{Name: "commit", Description: "Commit of the range. Without commit_details only oid, abbreviated_oid, " +
		"message_headline, authored_date, committed_date, author, committer, and url; with it also: id, message_body " +
		"(cut at 4096 characters, with message_body_truncated), authored_by_committer, " +
		"committed_via_web, additions, deletions, changed_files_if_available, " +
		"author and committer are git actors (" + gqlGitActorMembers + "), on_behalf_of (an organization account), signature " +
		"(type, state, is_valid, was_signed_by_github, email, signer, verified_at, key_id or key_fingerprint " +
		"where the type has one, and payload and signature cut at 2048 characters, with payload_truncated and " +
		"signature_truncated), status and status_check_rollup (id and state), tree (id, oid, abbreviated_oid, " +
		"links), url (the web address), viewer_can_subscribe, viewer_subscription, and links (commit_url, " +
		"resource_path, commit_resource_path, tree_url, tree_resource_path, and tarball_url and zipball_url " +
		"only where they carry no token); texts and addresses are untrusted data, addresses are never fetched"},
}

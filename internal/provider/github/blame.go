package github

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The blame tool reads, through GraphQL, the commit that last changed each line of one file of a repository
// at one ref, over a bounded line range. GitHub's blame field never returns a line's content, only the
// range of lines one commit covers, so this tool never carries file content beyond, or even within, the
// requested range.

// Bounds of the blame tool: the default line range and the largest one a request may name.
const (
	defaultBlameLines = 100
	maxBlameLines     = 2000
)

const lineSchema = `{"type":"integer","minimum":1,"maximum":100000000}`

// blameQuery asks for every range GitHub's blame reports for a file at a ref; checkBlameArguments bounds the
// range this tool answers with, clipping GitHub's ranges to it after the request, since blame carries no
// line-range argument of its own.
const blameQuery = `query($owner:String!,$name:String!,$ref:String!,$path:String!){` +
	`repository(owner:$owner,name:$name){object(expression:$ref){... on Commit{` +
	`blame(path:$path){ranges{startingLine endingLine commit{oid message author{name date user{login}}}}}}}}}`

const blameRangeProperties = `"start_line":{"type":"integer"},"end_line":{"type":"integer"},` +
	`"sha":{"type":"string"},"author":{"type":"string"},"date":{"type":"string"}`

const blameRangeRequired = `"required":["start_line","end_line","sha"],"additionalProperties":false`

var blameGet = capability.Descriptor{
	ID:      Provider + ".blame.get",
	Version: 1,
	Title:   "Get the GitHub blame of a file",
	Description: "Read, over a bounded line range, the commit that last changed each line of one file of a " +
		"repository an explicit connection allows at a ref, with the commit's SHA, author, and date; carries " +
		"no file content beyond, or even within, the range, since GitHub's blame never returns one",
	Tags:                       []string{"github", "blame", "contents", "repository", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"path":`+contentsPathSchema+`,"ref":`+refSchema+`,"start_line":`+lineSchema+
		`,"end_line":`+lineSchema, "path"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"ranges":{"type":"array","items":{` +
		`"type":"object","properties":{` + blameRangeProperties + `},` + blameRangeRequired + `}},` +
		`"start_line":{"type":"integer"},"end_line":{"type":"integer"}},` +
		`"required":["ranges","start_line","end_line"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "path", Description: "File inside the repository", Required: true},
		{Name: "ref", Description: "Branch, tag, or commit SHA; the default branch's tip when omitted"},
		{Name: "start_line", Description: "First line to blame, from 1; 1 when omitted"},
		{Name: "end_line", Description: fmt.Sprintf("Last line to blame; start_line+%d-1 when omitted, at "+
			"most %d lines from start_line", defaultBlameLines, maxBlameLines)},
	},
	Fields: []capability.Field{
		{Name: "ranges", Description: "Commit ranges clipped to start_line..end_line, each with its commit " +
			"sha, author, and date, without file content"},
		{Name: "start_line", Description: "First line this batch covers"},
		{Name: "end_line", Description: "Last line this batch covers"},
	},
	Examples: []capability.Example{{
		Description: "Blame the first 50 lines of a file",
		Arguments:   json.RawMessage(`{"path":"internal/app.go","start_line":1,"end_line":50}`),
	}},
}

func blameOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: blameGet, Handler: capability.Handler(invokeBlameGet)},
	}
}

// blameArguments are the path, the ref, and the line range of one github.blame.get call.
type blameArguments struct {
	Path      string `json:"path"`
	Ref       string `json:"ref"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

// checkBlameArguments applies the path and ref checks and the default and bound of the line range before a
// credential is resolved.
func checkBlameArguments(a *blameArguments) error {
	if !validContentsPath(a.Path) {
		return invalidRequest("path must not start or end with /, and must carry no empty, \".\", or \"..\" " +
			"segment, or control character")
	}
	if a.Ref != "" && !validRef(a.Ref) {
		return invalidRequest("ref must be a branch, a tag, or a commit SHA")
	}
	if a.StartLine == 0 {
		a.StartLine = 1
	}
	if a.EndLine == 0 {
		a.EndLine = a.StartLine + defaultBlameLines - 1
	}
	switch {
	case a.EndLine < a.StartLine:
		return invalidRequest("end_line must not lie before start_line")
	case a.EndLine-a.StartLine+1 > maxBlameLines:
		return invalidRequest(fmt.Sprintf("the range from start_line to end_line must not exceed %d lines", maxBlameLines))
	}
	return nil
}

func invokeBlameGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments blameArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("get file blame")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkBlameArguments(&arguments); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.getBlame(ctx, arguments))
}

// blameReadPermission names what a token needs to read a file's blame. GitHub decides on every request; the
// message names what such a request needs without claiming what the configured token holds.
const blameReadPermission = "GitHub refused this token the blame of this file; reading it needs no scope " +
	"for a public repository, or repo on a classic token, or Contents: read on a fine-grained token, for a " +
	"private one"

// BlameRange is one line range of a file's blame, clipped to the requested start_line..end_line.
type BlameRange struct {
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	SHA       string `json:"sha"`
	Author    string `json:"author,omitempty"`
	Date      string `json:"date,omitempty"`
}

// BlameResult is the blame of one file over one line range.
type BlameResult struct {
	Ranges    []BlameRange `json:"ranges"`
	StartLine int          `json:"start_line"`
	EndLine   int          `json:"end_line"`
}

type blamePageJSON struct {
	Repository *struct {
		Object *struct {
			Typename string `json:"__typename"`
			Blame    *struct {
				Ranges []struct {
					StartingLine int `json:"startingLine"`
					EndingLine   int `json:"endingLine"`
					Commit       struct {
						OID    string `json:"oid"`
						Author struct {
							Name string `json:"name"`
							Date string `json:"date"`
							User *struct {
								Login string `json:"login"`
							} `json:"user"`
						} `json:"author"`
					} `json:"commit"`
				} `json:"ranges"`
			} `json:"blame"`
		} `json:"object"`
	} `json:"repository"`
}

// getBlame reads the blame of one file of the bound repository at one ref, clipping GitHub's ranges to the
// requested start_line..end_line. A ref GitHub cannot resolve, or a path with no blame at that ref, answers
// with data but no error, so both are checked explicitly and reported as not-found.
func (c *Client) getBlame(ctx context.Context, a blameArguments) (*BlameResult, error) {
	const op = "get file blame"
	ref := a.Ref
	if ref == "" {
		ref = "HEAD"
	}
	variables := map[string]any{"owner": c.target.owner, "name": c.target.repo, "ref": ref, "path": a.Path}
	var page blamePageJSON
	if err := c.graphql(ctx, op, blameQuery, variables, &page); err != nil {
		return nil, actionsFailure(err, blameReadPermission)
	}
	if page.Repository == nil {
		return nil, notFound(op, subject{in: c.target})
	}
	if page.Repository.Object == nil {
		return nil, notFound(op, subject{in: c.target, what: "ref " + ref})
	}
	if page.Repository.Object.Typename != "Commit" || page.Repository.Object.Blame == nil {
		return nil, notFound(op, subject{in: c.target, what: "path " + a.Path + " at ref " + ref})
	}
	result := &BlameResult{Ranges: []BlameRange{}, StartLine: a.StartLine, EndLine: a.EndLine}
	for _, r := range page.Repository.Object.Blame.Ranges {
		start, end := max(r.StartingLine, a.StartLine), min(r.EndingLine, a.EndLine)
		if start > end {
			continue
		}
		if r.Commit.OID == "" {
			return nil, invalidEntry(op, "a blame range")
		}
		entry := BlameRange{StartLine: start, EndLine: end, SHA: r.Commit.OID, Author: r.Commit.Author.Name,
			Date: r.Commit.Author.Date}
		if r.Commit.Author.User != nil && r.Commit.Author.User.Login != "" {
			entry.Author = r.Commit.Author.User.Login
		}
		result.Ranges = append(result.Ranges, entry)
	}
	return result, nil
}

package bookstack

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// BookStack resolves page includes ({{@ID}} and {{@ID#section}}) in the rendered html by what the token may
// read, not by the targets. On a connection with targets an include of a page outside them would put foreign
// content into html. The rules below follow BookStack's include parser so that a page without such an include
// keeps BookStack's html unchanged, and one with such an include gets an html built here in which that include
// stays empty.
const (
	// maxIncludeDepth is the number of passes BookStack runs over a page: a tag inside content included in the
	// last pass stays text.
	maxIncludeDepth = 3
	// maxIncludedPages bounds the included pages one call reads.
	maxIncludedPages = 50
	// maxIncludedBytes bounds both the stored HTML read for includes and the content inserted while building
	// html, so that repeated includes cannot grow a call without limit.
	maxIncludedBytes = maxReadResponseBytes
)

// includeTag is BookStack's include pattern; \v is added because PCRE's \s matches it and Go's does not.
var includeTag = regexp.MustCompile(`\{\{@[\t\n\v\f\r ]?([0-9].*?)\}\}`)

// errIncludeLimit reports that the includes of a page exceed what one call checks.
var errIncludeLimit = errors.New("the page includes more content than one call checks")

// blockIncludeTags are the section elements BookStack inserts as a whole block instead of their children.
var blockIncludeTags = []string{"table", "ul", "ol", "pre"}

// includedPage is one included page as this call resolved it: in reach with its stored HTML, or empty.
type includedPage struct {
	raw     string
	body    *html.Node
	parsed  bool
	outside bool
}

// outsideInclude stands for every include that names no page this call can prove.
var outsideInclude = &includedPage{outside: true}

// includeRef reads the page and section of an include tag as BookStack does: the page is PHP's integer value of
// the part before '#', the section the trimmed rest. ok is false for a value that is no plain identifier,
// including forms such as 1e3 or 1.5 that PHP reads as another number; such a tag is treated as outside.
func includeRef(inner string) (id int64, section string, ok bool) {
	head, section, _ := strings.Cut(inner, "#")
	section = strings.Trim(section, " \t\n\r\x00\x0b")
	rest := strings.TrimLeft(head, "0123456789")
	if rest != "" && strings.ContainsRune(".eE", rune(rest[0])) {
		return 0, section, false
	}
	id, ok = parseID(strings.TrimLeft(head[:len(head)-len(rest)], "0"))
	return id, section, ok
}

// includeCandidates returns the inner text of every include tag in raw as written and as entity-decoded. That
// covers text, attributes, comments, and raw text alike, a superset of the tags BookStack's parser finds.
func includeCandidates(raw string) []string {
	var out []string
	for _, text := range []string{raw, html.UnescapeString(raw)} {
		for _, m := range includeTag.FindAllStringSubmatch(text, -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// scopeHTML replaces page.HTML with an html built from page.RawHTML when an include BookStack may have resolved
// names a page outside the targets. Without targets or without such an include it changes nothing.
func (c *Client) scopeHTML(ctx context.Context, page *pageJSON) error {
	candidates := includeCandidates(page.RawHTML)
	if !c.scope.bound() || len(candidates) == 0 {
		return nil
	}
	r := &includeRenderer{ctx: ctx, c: c, seen: map[[2]int64]bool{}}
	outside, err := r.outsideIn(candidates, 1)
	if err != nil {
		return err
	}
	built, err := r.render(page.RawHTML)
	if err != nil {
		return err
	}
	if outside || r.outside {
		page.HTML = built
	}
	return nil
}

// withholdHTML is the html of a page whose includes could not be checked: no html at all, and marked.
func withholdHTML(page *pageJSON) {
	page.HTML = ""
	page.htmlWithheld = true
}

// includedPage resolves one include for this call. A page the targets exclude by its identifier is decided
// without a read; any other one is read once, which is both the evidence of its reach and its stored HTML.
// A page the token cannot read is empty, as in BookStack.
func (c *Client) includedPage(ctx context.Context, id int64) (*includedPage, error) {
	if p, ok := c.included[id]; ok {
		return p, nil
	}
	var read struct {
		bookRef
		RawHTML string `json:"raw_html"`
	}
	fetched, missing := false, false
	fetch := func() error {
		if c.includeReads >= maxIncludedPages {
			return errIncludeLimit
		}
		c.includeReads++
		fetched = true
		err := c.get(ctx, "get page", "/api/pages/"+strconv.FormatInt(id, 10), nil, &read, nil, provider.ClassPermission)
		var perr *provider.Error
		missing = errors.As(err, &perr) && perr.Class == provider.ClassNotFound
		return err
	}
	in, err := c.pageReached(ctx, id, func() (bookRef, error) {
		err := fetch()
		return read.bookRef, err
	})
	if err == nil && in && !fetched {
		err = fetch()
	}
	p := &includedPage{outside: !in}
	if in {
		p.raw = read.RawHTML
	}
	// Only the page's own read may make it empty; a shelf that cannot be read as evidence fails the call.
	switch {
	case missing:
		p = &includedPage{}
	case err != nil:
		return nil, err
	}
	if c.includedBytes += len(p.raw); c.includedBytes > maxIncludedBytes {
		return nil, errIncludeLimit
	}
	if c.included == nil {
		c.included = map[int64]*includedPage{}
	}
	c.included[id] = p
	return p, nil
}

// includeRenderer builds the html of one page.
type includeRenderer struct {
	ctx context.Context
	c   *Client
	// seen holds the pages whose stored HTML was already searched at a depth.
	seen     map[[2]int64]bool
	inserted int
	cleanup  []*html.Node
	// outside records that an include resolved while building named a page outside the targets.
	outside bool
}

func (r *includeRenderer) page(inner string) (*includedPage, string, error) {
	id, section, ok := includeRef(inner)
	if !ok {
		return outsideInclude, section, nil
	}
	p, err := r.c.includedPage(r.ctx, id)
	return p, section, err
}

// outsideIn reports whether a candidate at depth, or one in the pages it includes up to the last depth, names a
// page outside the targets.
func (r *includeRenderer) outsideIn(candidates []string, depth int) (bool, error) {
	for _, inner := range candidates {
		p, _, err := r.page(inner)
		if err != nil || p.outside {
			return p != nil && p.outside, err
		}
		id, _, _ := includeRef(inner)
		if depth == maxIncludeDepth || r.seen[[2]int64{id, int64(depth)}] {
			continue
		}
		r.seen[[2]int64{id, int64(depth)}] = true
		if out, err := r.outsideIn(includeCandidates(p.raw), depth+1); out || err != nil {
			return out, err
		}
	}
	return false, nil
}

// render runs BookStack's include passes over raw, with every include outside the targets left empty, and
// returns the inner HTML of the body.
func (r *includeRenderer) render(raw string) (string, error) {
	body := parseBody(raw)
	added := 1
	for depth := 0; depth < maxIncludeDepth && added != 0; depth++ {
		var err error
		if added, err = r.pass(body); err != nil {
			return "", err
		}
	}
	var out strings.Builder
	for n := range body.ChildNodes() {
		if err := html.Render(&out, n); err != nil {
			return "", err
		}
	}
	return out.String(), nil
}

func parseBody(raw string) *html.Node {
	body := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, _ := html.ParseFragment(strings.NewReader(raw), body)
	for _, n := range nodes {
		body.AppendChild(n)
	}
	return body
}

type includeTagNode struct {
	inner string
	node  *html.Node
}

// pass is one run of BookStack's parser: isolate every tag in a text node, replace it by its content, and
// clean up the elements a block include split or emptied. It returns the number of nodes inserted.
func (r *includeRenderer) pass(body *html.Node) (int, error) {
	var hosts []*html.Node
	if hostsInclude(body) {
		hosts = append(hosts, body)
	}
	for n := range body.Descendants() {
		if n.Type == html.ElementNode && hostsInclude(n) {
			hosts = append(hosts, n)
		}
	}
	var tags []includeTagNode
	for _, host := range hosts {
		for _, child := range slices.Collect(host.ChildNodes()) {
			if child.Type == html.TextNode {
				tags = append(tags, isolateTags(child)...)
			}
		}
	}
	added := 0
	for _, tag := range tags {
		if tag.node.Parent == nil {
			continue
		}
		content, inline, err := r.content(tag.inner)
		if err != nil {
			return 0, err
		}
		if !inline {
			if p := paragraphOf(tag.node); p != nil && p == tag.node.Parent {
				r.split(p, tag.node)
			} else if p != nil {
				r.moveBeside(tag, p)
			}
		}
		added += len(content)
		for _, n := range content {
			tag.node.Parent.InsertBefore(n, tag.node)
		}
		tag.node.Parent.RemoveChild(tag.node)
	}
	for _, n := range r.cleanup {
		normalize(n)
		for n.Parent != nil && n.FirstChild == nil {
			parent := n.Parent
			parent.RemoveChild(n)
			n = parent
		}
	}
	r.cleanup = nil
	return added, nil
}

func hostsInclude(n *html.Node) bool {
	for child := range n.ChildNodes() {
		if child.Type == html.TextNode && strings.Contains(child.Data, "{{@") {
			return true
		}
	}
	return false
}

// isolateTags splits a text node so that each tag in it is a text node of its own.
func isolateTags(text *html.Node) []includeTagNode {
	var tags []includeTagNode
	data, offset := text.Data, 0
	for _, m := range includeTag.FindAllStringSubmatchIndex(data, -1) {
		if offset < m[0] {
			text.Parent.InsertBefore(&html.Node{Type: html.TextNode, Data: data[offset:m[0]]}, text)
		}
		node := &html.Node{Type: html.TextNode, Data: data[m[0]:m[1]]}
		text.Parent.InsertBefore(node, text)
		tags = append(tags, includeTagNode{inner: data[m[2]:m[3]], node: node})
		offset = m[1]
	}
	if offset > 0 {
		text.Data = data[offset:]
	}
	return tags
}

// content returns fresh copies of what a tag inserts and whether BookStack inserts it inline.
func (r *includeRenderer) content(inner string) ([]*html.Node, bool, error) {
	p, section, err := r.page(inner)
	if err != nil {
		return nil, false, err
	}
	if p.outside {
		r.outside = true
	}
	if p.outside || p.raw == "" || p.raw == "0" {
		return nil, true, nil
	}
	if !p.parsed {
		p.body, p.parsed = parseBody(p.raw), true
	}
	if section == "" || section == "0" {
		nodes, err := r.copies(slices.Collect(p.body.ChildNodes()))
		return nodes, false, err
	}
	found := elementByID(p.body, section)
	switch {
	case found == nil:
		return nil, true, nil
	case slices.Contains(blockIncludeTags, strings.ToLower(found.Data)):
		nodes, err := r.copies([]*html.Node{found})
		return nodes, false, err
	}
	nodes, err := r.copies(slices.Collect(found.ChildNodes()))
	return nodes, true, err
}

// elementByID finds the element libxml's getElementById returns in an HTML document: the first one whose id,
// or whose name on an a element, equals id.
func elementByID(root *html.Node, id string) *html.Node {
	for n := range root.Descendants() {
		if n.Type != html.ElementNode {
			continue
		}
		for _, a := range n.Attr {
			if a.Namespace == "" && a.Val == id && (a.Key == "id" || (a.Key == "name" && n.Data == "a")) {
				return n
			}
		}
	}
	return nil
}

func (r *includeRenderer) copies(nodes []*html.Node) ([]*html.Node, error) {
	out := make([]*html.Node, 0, len(nodes))
	for _, n := range nodes {
		copied, err := r.copyNode(n)
		if err != nil {
			return nil, err
		}
		out = append(out, copied)
	}
	return out, nil
}

func (r *includeRenderer) copyNode(n *html.Node) (*html.Node, error) {
	m := shallowCopy(n)
	r.inserted += len(n.Data) + 1
	for _, a := range n.Attr {
		r.inserted += len(a.Key) + len(a.Val)
	}
	if r.inserted > maxIncludedBytes {
		return nil, errIncludeLimit
	}
	for child := range n.ChildNodes() {
		copied, err := r.copyNode(child)
		if err != nil {
			return nil, err
		}
		m.AppendChild(copied)
	}
	return m, nil
}

func shallowCopy(n *html.Node) *html.Node {
	return &html.Node{Type: n.Type, DataAtom: n.DataAtom, Data: n.Data, Namespace: n.Namespace,
		Attr: slices.Clone(n.Attr)}
}

// paragraphOf returns the nearest p element that holds n.
func paragraphOf(n *html.Node) *html.Node {
	for ; n != nil; n = n.Parent {
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "p") {
			return n
		}
	}
	return nil
}

// split moves the children of p before the tag into a copy of p without its id, and the tag between both.
func (r *includeRenderer) split(p, tag *html.Node) {
	clone := shallowCopy(p)
	clone.Attr = slices.DeleteFunc(clone.Attr, func(a html.Attribute) bool { return a.Namespace == "" && a.Key == "id" })
	p.Parent.InsertBefore(clone, p)
	for child := p.FirstChild; child != nil && child != tag; child = p.FirstChild {
		p.RemoveChild(child)
		clone.AppendChild(child)
	}
	p.RemoveChild(tag)
	p.Parent.InsertBefore(tag, p)
	r.cleanup = append(r.cleanup, p, clone)
}

// moveBeside moves a tag nested deeper in p before or after p, by where its text stands in p.
func (r *includeRenderer) moveBeside(tag includeTagNode, p *html.Node) {
	text := textContent(p)
	pos := strings.Index(text, tag.inner)
	r.cleanup = append(r.cleanup, tag.node.Parent)
	tag.node.Parent.RemoveChild(tag.node)
	if pos < 0 || 2*pos < len(text) {
		p.Parent.InsertBefore(tag.node, p)
	} else {
		p.Parent.InsertBefore(tag.node, p.NextSibling)
	}
}

func textContent(n *html.Node) string {
	var b strings.Builder
	for d := range n.Descendants() {
		if d.Type == html.TextNode {
			b.WriteString(d.Data)
		}
	}
	return b.String()
}

// normalize merges adjacent text nodes and drops empty ones in the subtree, like DOM normalize. A tag split
// over two text nodes becomes whole again here and is found by the next pass, as in BookStack.
func normalize(n *html.Node) {
	for child := n.FirstChild; child != nil; {
		next := child.NextSibling
		switch child.Type {
		case html.TextNode:
			for next != nil && next.Type == html.TextNode {
				child.Data += next.Data
				after := next.NextSibling
				n.RemoveChild(next)
				next = after
			}
			if child.Data == "" {
				n.RemoveChild(child)
			}
		case html.ElementNode:
			normalize(child)
		}
		child = next
	}
}

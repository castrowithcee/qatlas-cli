package bookstack

const (
	// maxTreeComments and maxCommentLevels bound the comment tree of a page read, over active and archived together.
	maxTreeComments  = 500
	maxCommentLevels = 10
)

const (
	commentTreeDescription = "Comment tree as active and archived lists of nodes with comment, depth, and children; at most 500 comments and 10 levels, personal and untrusted data"
	commentTreeSchema      = `{"type":"object","properties":{"active":{"type":"array"},"archived":{"type":"array"}}}`
)

// commentNodeJSON is one node of the comment tree of a page.
type commentNodeJSON struct {
	Comment  commentJSON       `json:"comment"`
	Children []commentNodeJSON `json:"children"`
}

type commentTreeJSON struct {
	Active   []commentNodeJSON `json:"active"`
	Archived []commentNodeJSON `json:"archived"`
}

// treeShown walks the tree and counts what it cuts.
type treeShown struct {
	left   int
	capped bool
}

// commentTree shows the comment tree of a page, or nil and false when the page has no comments. The second
// result reports that comments or comment text were cut.
func commentTree(t *commentTreeJSON) (map[string]any, bool) {
	if t == nil || len(t.Active)+len(t.Archived) == 0 {
		return nil, false
	}
	s := &treeShown{left: maxTreeComments}
	// A comment text counts as cut in the same way as in comments.get, see clipBytes.
	active := s.nodes(t.Active, 0)
	archived := s.nodes(t.Archived, 0)
	return map[string]any{"active": active, "archived": archived}, s.capped
}

func (s *treeShown) nodes(in []commentNodeJSON, level int) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, n := range in {
		if level >= maxCommentLevels || s.left <= 0 {
			s.capped = true
			break
		}
		s.left--
		out = append(out, map[string]any{
			"comment": s.comment(n.Comment), "depth": level, "children": s.nodes(n.Children, level+1),
		})
	}
	return out
}

// comment maps a comment of the tree with the name BookStack gives each field. A top-level comment has no
// parent_id and an unattached one no content_ref.
func (s *treeShown) comment(m commentJSON) map[string]any {
	html, cut := clipBytes(m.HTML, maxCommentHTMLBytes)
	s.capped = s.capped || cut
	out := map[string]any{
		"id": m.ID, "commentable_id": m.CommentableID, "commentable_type": clip(m.CommentableType, maxResultString),
		"local_id": m.LocalID, "archived": m.Archived, "html": html,
		"created_at": m.CreatedAt, "updated_at": m.UpdatedAt,
	}
	if m.ParentID != nil {
		out["parent_id"] = *m.ParentID
	}
	if m.ContentRef != "" {
		out["content_ref"] = clip(m.ContentRef, maxContentRefChars)
	}
	if m.CreatedBy != nil {
		out["created_by"] = userDetail(m.CreatedBy)
	}
	if m.UpdatedBy != nil {
		out["updated_by"] = map[string]any{"id": m.UpdatedBy.ID}
	}
	return out
}

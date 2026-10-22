package bookstack

// coverJSON is the cover image of a book or shelf. A list gives id, name and url; a read adds the rest.
type coverJSON struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	URL        string    `json:"url"`
	Path       string    `json:"path"`
	Type       string    `json:"type"`
	UploadedTo *int64    `json:"uploaded_to"`
	CreatedAt  string    `json:"created_at"`
	UpdatedAt  string    `json:"updated_at"`
	CreatedBy  *userJSON `json:"created_by"`
	UpdatedBy  *userJSON `json:"updated_by"`
}

const (
	coverListDescription   = "Cover image as id, name, and url, an untrusted address of the instance that is never fetched; absent without a cover"
	coverDetailDescription = "Cover image as id, name, url, path (untrusted addresses of the instance that are never fetched), type, uploaded_to, timestamps, and creator and last editor as id only; absent without a cover"

	coverDetailSchema = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"url":{"type":"string"},"path":{"type":"string"},"type":{"type":"string"},"uploaded_to":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"created_by":{"type":"object","properties":{"id":{"type":"integer"}}},"updated_by":{"type":"object","properties":{"id":{"type":"integer"}}}}}`
)

// coverBase is the part of a cover shown in a list and in a read.
func coverBase(c *coverJSON) map[string]any {
	out := map[string]any{"id": c.ID, "name": clip(c.Name, maxResultString)}
	if c.URL != "" {
		out["url"] = clip(c.URL, maxResultString)
	}
	return out
}

// addListCover sets the cover of a list row; a null cover stays out.
func addListCover(row map[string]any, c *coverJSON) {
	if c != nil {
		row["cover"] = coverBase(c)
	}
}

// coverDetail is the cover of a read, or nil without one. Addresses are shown as text and never fetched.
func coverDetail(c *coverJSON) map[string]any {
	if c == nil {
		return nil
	}
	out := coverBase(c)
	if c.Path != "" {
		out["path"] = clip(c.Path, maxResultString)
	}
	if c.Type != "" {
		out["type"] = clip(c.Type, maxResultString)
	}
	if c.UploadedTo != nil {
		out["uploaded_to"] = *c.UploadedTo
	}
	if c.CreatedAt != "" {
		out["created_at"] = c.CreatedAt
	}
	if c.UpdatedAt != "" {
		out["updated_at"] = c.UpdatedAt
	}
	if c.CreatedBy != nil {
		out["created_by"] = map[string]any{"id": c.CreatedBy.ID}
	}
	if c.UpdatedBy != nil {
		out["updated_by"] = map[string]any{"id": c.UpdatedBy.ID}
	}
	return out
}

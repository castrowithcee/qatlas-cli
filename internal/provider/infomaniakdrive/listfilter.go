package infomaniakdrive

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// Filter values the three list operations document; the order_by values differ per operation.
var (
	listTypeValues      = []string{"dir", "file", "vault"}
	listRightValues     = []string{"inherit", "password", "public"}
	listDirectionValues = []string{"asc", "desc"}

	filesOrderByValues = []string{"added_at", "last_modified_at", "mime_type", "name", "revised_at", "size", "type", "updated_at"}
	trashOrderByValues = []string{"added_at", "deleted_at", "last_modified_at", "mime_type", "name", "revised_at", "size", "type", "updated_at"}
	linksOrderByValues = []string{"added_at", "created_at", "expires_at", "has_password", "last_modified_at", "mime_type", "name",
		"revised_at", "shared_by", "size", "type", "updated_at"}
)

// Cursor sizes: Infomaniak's own cursor is bounded by maxInnerCursorLength; the Qatlas cursor adds the
// filter fingerprint and is base64url encoded without padding.
const (
	maxInnerCursorLength   = 2048
	cursorFingerprintBytes = 16
	maxCursorLength        = (maxInnerCursorLength + cursorFingerprintBytes) * 4 / 3
)

var errCursorMismatch = "cursor does not belong to these type, order_by, order, order_for and right arguments or is no " +
	"longer valid; read the first page again without a cursor"

// listFilter is the structured type, sort and share-right selection of one list call.
type listFilter struct {
	Type     []string          `json:"type"`
	OrderBy  []string          `json:"order_by"`
	Order    string            `json:"order"`
	OrderFor map[string]string `json:"order_for"`
	Right    []string          `json:"right"`
}

// listFilterInput is the JSON Schema fragment of the filter arguments; right only where the operation has it.
func listFilterInput(orderBy []string, withRight bool) string {
	set := func(values []string) string {
		return `{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(len(values)) +
			`,"uniqueItems":true,"items":{"type":"string","enum":` + quotedList(values) + `}}`
	}
	direction := `{"type":"string","enum":` + quotedList(listDirectionValues) + `}`
	var forProps []string
	for _, name := range orderBy {
		forProps = append(forProps, strconv.Quote(name)+`:`+direction)
	}
	schema := `"type":` + set(listTypeValues) + `,"order_by":` + set(orderBy) + `,"order":` + direction +
		`,"order_for":{"type":"object","minProperties":1,"properties":{` + strings.Join(forProps, ",") +
		`},"additionalProperties":false}`
	if withRight {
		schema += `,"right":` + set(listRightValues)
	}
	return schema
}

// listFilterArguments describes the filter arguments for the descriptor.
func listFilterArguments(withRight bool) []capability.Argument {
	args := []capability.Argument{
		{Name: "type", Description: "Only entries of these structure types; sent as Infomaniak's own type filter. Entries of a vault still report file or folder"},
		{Name: "order_by", Description: "Ordered attributes to sort by, first has priority, without repetition"},
		{Name: "order", Description: "Default sort direction, asc or desc, for every order_by attribute"},
		{Name: "order_for", Description: "Sort direction per attribute; keys must also be listed in order_by"},
	}
	if withRight {
		args = append(args, capability.Argument{Name: "right", Description: "Only links with these access rights: inherit, password or public"})
	}
	return args
}

// validate checks the filter against the values one operation offers. It runs before any secret access.
func (f listFilter) validate(orderBy []string, withRight bool) error {
	if err := checkValues(f.Type, listTypeValues, "type"); err != nil {
		return err
	}
	if err := checkValues(f.OrderBy, orderBy, "order_by"); err != nil {
		return err
	}
	if f.Order != "" && !slices.Contains(listDirectionValues, f.Order) {
		return invalidRequest("order must be asc or desc")
	}
	for key, direction := range f.OrderFor {
		if !slices.Contains(orderBy, key) || !slices.Contains(f.OrderBy, key) {
			return invalidRequest("order_for keys must be attributes this tool offers and also listed in order_by")
		}
		if !slices.Contains(listDirectionValues, direction) {
			return invalidRequest("order_for directions must be asc or desc")
		}
	}
	if len(f.Right) > 0 && !withRight {
		return invalidRequest("right is not offered by this tool")
	}
	return checkValues(f.Right, listRightValues, "right")
}

// apply adds the filter to the request: repeated parameters for the lists, order_for[key] per key.
func (f listFilter) apply(query url.Values) {
	if len(f.Type) > 0 {
		query["type"] = slices.Clone(f.Type)
	}
	if len(f.OrderBy) > 0 {
		query["order_by"] = slices.Clone(f.OrderBy)
	}
	if f.Order != "" {
		query.Set("order", f.Order)
	}
	for key, direction := range f.OrderFor {
		query.Set("order_for["+key+"]", direction)
	}
	if len(f.Right) > 0 {
		query["right"] = slices.Clone(f.Right)
	}
}

// fingerprint identifies the filters a cursor was issued for. Sets are order independent; order_by keeps its
// order because the order is its priority. limit and with are not part of it.
func (f listFilter) fingerprint() []byte {
	sorted := func(values []string) []string {
		return slices.Sorted(slices.Values(append([]string{}, values...)))
	}
	forPairs := make([][2]string, 0, len(f.OrderFor))
	for _, key := range slices.Sorted(func(yield func(string) bool) {
		for k := range f.OrderFor {
			if !yield(k) {
				return
			}
		}
	}) {
		forPairs = append(forPairs, [2]string{key, f.OrderFor[key]})
	}
	encoded, _ := json.Marshal([]any{sorted(f.Type), append([]string{}, f.OrderBy...), f.Order, forPairs, sorted(f.Right)})
	sum := sha256.Sum256(encoded)
	return sum[:cursorFingerprintBytes]
}

// openCursor returns Infomaniak's own cursor from a Qatlas cursor, or an invalid request when the cursor is
// unreadable, stems from other filters, or predates the binding. An empty cursor stays empty.
func (f listFilter) openCursor(cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) <= cursorFingerprintBytes || len(raw) > cursorFingerprintBytes+maxInnerCursorLength ||
		!bytes.Equal(raw[:cursorFingerprintBytes], f.fingerprint()) {
		return "", invalidRequest(errCursorMismatch)
	}
	return string(raw[cursorFingerprintBytes:]), nil
}

// sealCursor binds Infomaniak's cursor to the filters; an empty cursor stays empty.
func (f listFilter) sealCursor(cursor string) string {
	if cursor == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(append(f.fingerprint(), cursor...))
}

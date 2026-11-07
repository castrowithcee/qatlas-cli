package httpbind

import (
	"encoding/json"

	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/awsquery"
)

// Pager and Page are the paging mechanism of the awsQuery runtime: NextToken lists with an optional page
// size (MaxResults or PageSize), a cursor bound to the tool and its arguments, and a refusal of a cursor
// that was altered or issued for other arguments. The mechanism does not depend on the protocol, so the REST
// protocols use it as it is. A Pager names its members by wire name (TokenIn, SizeParam) and by key
// (TokenOut), which are the Name and Key of the Fields of the description.
type (
	Pager = awsquery.Pager
	Page  = awsquery.Page
)

// The paging members belong in the description of the input, where their place (query or body) is bound,
// under a key that starts with "~" (as the output members of the pager do), so that they never collide with
// an argument of the tool and the tool reads page_size and cursor instead.
//
// ApplyPage returns args with the paging members of page set: the page size and the token, found among the
// top-level members of the input by their wire name, in the query or in the body. A paging member the input
// lacks, or an argument that already holds one, is an error: the description or the call would otherwise
// disagree with the cursor.
func ApplyPage(input *Shape, args map[string]any, page Page) (map[string]any, error) {
	out := make(map[string]any, len(args)+2)
	for k, v := range args {
		out[k] = v
	}
	for name, value := range page.Params() {
		var field *Field
		if input != nil {
			for i := range input.Fields {
				f := &input.Fields[i]
				if f.Name == name && (f.Loc == LocBody || f.Loc == LocQuery) {
					field = f
				}
			}
		}
		if field == nil {
			return nil, shapeErr("the input has no paging member %q", name)
		}
		if _, dup := out[field.Key]; dup {
			return nil, valueErr("%s collides with the paging of the tool", field.Key)
		}
		if field.Shape.Kind == KindInteger {
			out[field.Key] = json.Number(value)
		} else {
			out[field.Key] = value
		}
	}
	return out, nil
}

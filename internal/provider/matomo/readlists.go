package matomo

import (
	"encoding/json"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The tools of insights, transitions, bot real time, and database statistics read answers that Matomo
// cannot window on the server. They share the report window: limit rows per list, 100 when omitted, at most
// 1000, continued with a cursor that is bound to the arguments.

const limitSchema = `"limit":{"type":"integer","minimum":1,"maximum":1000},` +
	`"cursor":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[A-Za-z0-9_-]+$"}`

func windowArguments(unit string) []capability.Argument {
	return []capability.Argument{
		{Name: "limit", Description: unit + " per batch, 1 through 1000; 100 when omitted; a continuation keeps the " +
			"limit of its first batch"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous batch with the same arguments; the first " +
			"batch when omitted"},
	}
}

var windowFields = []capability.Field{
	{Name: "has_more", Description: "True when Matomo returned entries beyond this batch; Matomo reports no total"},
	{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
}

// startWindow reads limit and cursor of a call that reads one table at a time.
func startWindow(binding []byte, limit int, cursor string) (reportWindow, error) {
	return startReportWindow(binding, limit, 1, cursor)
}

// cutWindow cuts this batch out of a list Matomo answered in full, and reports whether rows follow it.
func cutWindow[T any](w reportWindow, rows []T) (batch []T, more bool) {
	rest := rows[min(w.offset, len(rows)):]
	if len(rest) > w.limit {
		return rest[:w.limit], true
	}
	return rest, false
}

// nextCursor is the cursor of the batch after this one, empty when nothing follows.
func (w reportWindow) next(more bool) string {
	if !more {
		return ""
	}
	return w.cursor(w.offset + w.limit)
}

// singlePeriod accepts the periods that Matomo answers with one table: one day, week, month, or year, or a
// range summed into one table.
func singlePeriod(period, date string) error {
	plan, err := planPeriod(period, date)
	if err != nil {
		return err
	}
	if plan.multiple {
		return invalidArgument("date must name one period; a range or lastN is read with period range")
	}
	return nil
}

// tableRows reads a DataTable answer as bounded rows. Matomo answers a table without rows as an empty list
// and a table of one row without dimension as one object.
func tableRows(op string, raw json.RawMessage) ([]map[string]any, error) {
	rows, ok := rowList(raw)
	if !ok {
		return nil, provider.InvalidResponse(op, "Matomo returned a table in a form this tool does not read")
	}
	out := make([]map[string]any, 0, len(rows))
	for _, one := range rows {
		out = append(out, reportRow(one))
	}
	return out, nil
}

func itoa(n int) string { return strconv.Itoa(n) }

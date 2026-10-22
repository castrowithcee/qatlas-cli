package twentycrm

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// pageTotal reads the optional top-level totalCount of a list answer. An absent or null count is no count;
// a negative or non-integer one is an unusable answer rather than a guess, and no float conversion happens.
func pageTotal(op string, raw json.RawMessage) (*int64, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	total, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || total < 0 {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable total count")
	}
	return &total, nil
}

// totalCountSchema is the output schema member of a list tool that reports the count Twenty gave.
const totalCountSchema = `"total_count":{"type":"integer","minimum":0}`

// totalCountField is the matching Fields entry.
var totalCountField = capability.Field{Name: "total_count", Description: "Number of entries Twenty counts for the whole list, absent when Twenty reports none"}

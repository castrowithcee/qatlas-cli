package infomaniakdrive

import "strconv"

// withInputSchema is the optional with argument of a read tool: a non-empty list of distinct values out of a
// fixed set.
func withInputSchema(values []string) string {
	return `"with":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(len(values)) +
		`,"uniqueItems":true,"items":{"type":"string","enum":` + quotedList(values) + `}}`
}

package awsquery

import (
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// MaxBodySize bounds a request body. The AWS query protocol sends everything in the POST body.
const MaxBodySize = 1 << 20

// Errors name the rule that failed, never the offending value.
var (
	ErrAction   = errors.New("awsquery: invalid Action name")
	ErrVersion  = errors.New("awsquery: invalid API version")
	ErrParam    = errors.New("awsquery: invalid parameter name")
	ErrTooLarge = errors.New("awsquery: request body exceeds the size limit")
)

var (
	actionName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,127}$`)
	versionID  = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	// A name may carry dots, so flattened keys such as Tags.member.1.Key (a later extension that only has to
	// produce such names) pass through unchanged.
	paramName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9.]{0,255}$`)
)

// EncodeBody builds the form body of a query-protocol call: Action and Version first, then the parameters
// sorted by name. params holds scalar values under their final, already flattened names.
func EncodeBody(action, version string, params map[string]string) ([]byte, error) {
	if !actionName.MatchString(action) {
		return nil, ErrAction
	}
	if !versionID.MatchString(version) {
		return nil, ErrVersion
	}
	names := make([]string, 0, len(params))
	for name := range params {
		if !paramName.MatchString(name) || name == "Action" || name == "Version" {
			return nil, ErrParam
		}
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("Action=" + url.QueryEscape(action) + "&Version=" + url.QueryEscape(version))
	for _, name := range names {
		b.WriteString("&" + url.QueryEscape(name) + "=" + url.QueryEscape(params[name]))
		if b.Len() > MaxBodySize {
			return nil, ErrTooLarge
		}
	}
	return []byte(b.String()), nil
}

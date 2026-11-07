package googlepagespeed

import (
	"net/url"
	"regexp"
	"strings"
)

// maxTargetBytes bounds a url or origin argument.
const maxTargetBytes = 2048

// Forms of the other arguments. They keep a value from adding a query member: nothing but these characters
// is sent. A value is never quoted in a refusal.
var (
	localePattern = regexp.MustCompile(`^[A-Za-z]{2,3}([_-][A-Za-z0-9]{2,8}){0,3}$`)
	campaign      = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,100}$`)
)

// checkTarget checks the form of a page that Google is asked to analyse or look up, and returns it
// unchanged. The page is never fetched by Qatlas; the form check keeps out other schemes (file:, ftp:,
// javascript:), user information that would put a credential into the request, and whitespace. With origin
// set, the value is only a scheme, a host, and an optional port, as the Chrome UX Report takes an origin;
// withoutFragment refuses a fragment, which the Chrome UX Report does not use.
func checkTarget(name, value string, origin, withoutFragment bool) (string, error) {
	lower := strings.ToLower(value)
	if value == "" || len(value) > maxTargetBytes || !(strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")) {
		return "", badRequest(name + " must be an absolute http or https address of at most 2048 characters")
	}
	for _, r := range value {
		if r <= ' ' || r == 0x7f {
			return "", badRequest(name + " must not contain spaces or control characters")
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.Opaque != "" {
		return "", badRequest(name + " must be an absolute http or https address with a host")
	}
	if parsed.User != nil || strings.Contains(parsed.Host, "@") {
		return "", badRequest(name + " must not contain user information")
	}
	if origin && (parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		strings.Contains(value, "#")) {
		return "", badRequest(name + " must be an origin: scheme, host, and optional port, without path, query, or fragment")
	}
	if withoutFragment && strings.Contains(value, "#") {
		return "", badRequest(name + " must not contain a fragment")
	}
	if origin {
		return strings.TrimSuffix(value, "/"), nil
	}
	return value, nil
}

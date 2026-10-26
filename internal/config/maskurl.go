package config

import (
	"net/url"
	"strings"
)

// SecretMask stands where a value that may be a secret would be shown.
const SecretMask = "***"

// SecretKeyName reports whether a key of an option, a query or a fragment names something that may hold a
// secret.
func SecretKeyName(key string) bool {
	key = strings.ToLower(key)
	for _, part := range []string{"token", "secret", "pass", "key", "auth", "cred", "sign", "bearer", "cookie", "session"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}

// MaskURL returns raw as it may be shown: the whole user info and the values of query or fragment pairs whose
// key names a secret are replaced by SecretMask. Host, path, keys and other values stay. Text that is no URL,
// or has no scheme, but carries an "@" or "?" is masked whole, since it cannot be masked with confidence. A
// fragment is read as pairs only when it holds an "=", otherwise it stays as it is.
func MaskURL(raw string) string {
	if !strings.ContainsAny(raw, "@?#") {
		return raw
	}
	u, err := url.Parse(raw)
	// "user:pw@host" parses with "user" as scheme and the rest opaque, so an opaque URL counts as none.
	if err != nil || u.Scheme == "" || u.Opaque != "" {
		if strings.ContainsAny(raw, "@?") {
			return SecretMask
		}
		return raw
	}
	if u.User != nil {
		// A token often stands as the user name alone, so the whole user info goes.
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(SecretMask, SecretMask)
		} else {
			u.User = url.User(SecretMask)
		}
	}
	u.RawQuery = maskPairs(u.RawQuery)
	if strings.Contains(u.Fragment, "=") {
		u.Fragment = maskPairs(u.Fragment)
		u.RawFragment = ""
	}
	return strings.ReplaceAll(u.String(), "%2A%2A%2A", SecretMask)
}

// maskPairs masks the value of every "key=value" pair of an "&"-separated list whose key names a secret.
func maskPairs(list string) string {
	if list == "" {
		return list
	}
	parts := strings.Split(list, "&")
	for i, part := range parts {
		if key, _, ok := strings.Cut(part, "="); ok && SecretKeyName(key) {
			parts[i] = key + "=" + SecretMask
		}
	}
	return strings.Join(parts, "&")
}

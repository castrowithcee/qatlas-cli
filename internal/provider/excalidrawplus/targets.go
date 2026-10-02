package excalidrawplus

import (
	"errors"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

const (
	collectionPrefix = "collection/"
	wildcard         = "*"
	// maxIDLength bounds a configured or argument identifier. Excalidraw+ documents its collection and scene
	// identifiers only as strings, so this length and the character set of validID are a local, deliberately
	// narrow choice that keeps an identifier one opaque path or query value, never a separator or a URL.
	maxIDLength = 64
)

// scope is the collection boundary of one connection: every collection the key can see (an explicit
// wildcard) or only the collections listed.
type scope struct {
	wildcard    bool
	collections []string
}

func (s scope) allows(collectionID string) bool {
	if s.wildcard {
		return true
	}
	if collectionID == "" {
		return false
	}
	for _, allowed := range s.collections {
		if allowed == collectionID {
			return true
		}
	}
	return false
}

// single returns the only allowed collection of a restricted scope, when there is exactly one.
func (s scope) single() (string, bool) {
	if s.wildcard || len(s.collections) != 1 {
		return "", false
	}
	return s.collections[0], true
}

// validID keeps an identifier to ASCII letters, digits, hyphen, and underscore.
func validID(raw string) bool {
	if raw == "" || len(raw) > maxIDLength {
		return false
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// parseScope reads the configured targets: the single wildcard, or one or more collection/COLLECTION_ID
// entries, none named twice. No error quotes a configured value.
func parseScope(values []string) (scope, error) {
	if len(values) == 0 {
		return scope{}, errors.New("an Excalidraw+ connection needs at least one collection/COLLECTION_ID target or the * wildcard")
	}
	if len(values) == 1 && strings.TrimSpace(values[0]) == wildcard {
		return scope{wildcard: true}, nil
	}
	var bound scope
	seen := map[string]bool{}
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == wildcard {
			return scope{}, errors.New("the Excalidraw+ * wildcard must be the only configured target")
		}
		id, err := parseTarget(raw)
		if err != nil {
			return scope{}, err
		}
		if seen[id] {
			return scope{}, errors.New("the Excalidraw+ collection allow-list names a collection more than once")
		}
		seen[id] = true
		bound.collections = append(bound.collections, id)
	}
	return bound, nil
}

func parseTarget(raw string) (string, error) {
	const form = "an Excalidraw+ target must be collection/COLLECTION_ID or *"
	if !strings.HasPrefix(raw, collectionPrefix) {
		return "", errors.New(form)
	}
	id := strings.TrimPrefix(raw, collectionPrefix)
	if !validID(id) {
		return "", errors.New(form)
	}
	return id, nil
}

func validateTarget(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == wildcard {
		return nil
	}
	_, err := parseTarget(raw)
	return err
}

func validateSet(values []string) error {
	_, err := parseScope(values)
	return err
}

// boundScope reads the connection's scope before any secret is resolved.
func boundScope(resolved *config.Resolved) (scope, error) {
	if resolved == nil {
		return scope{}, providerError("open", "no connection was selected")
	}
	values := resolved.Targets
	if len(values) == 0 && strings.TrimSpace(resolved.Target) != "" {
		values = []string{resolved.Target}
	}
	bound, err := parseScope(values)
	if err != nil {
		return scope{}, providerError("open", err.Error())
	}
	return bound, nil
}

// selectCollection checks a collection_id argument against the connection's allow-list before any secret is
// resolved and before any request is sent. The refusal never names the collection.
func selectCollection(resolved *config.Resolved, collectionID string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !validID(collectionID) {
		return invalidRequest("collection_id is not a valid identifier")
	}
	if !bound.allows(collectionID) {
		return invalidRequest("collection_id is outside the targets of this connection")
	}
	return nil
}

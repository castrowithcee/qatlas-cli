package excalidrawplus

import (
	"errors"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	collectionPrefix = "collection/"
	scenePrefix      = "scene/"
	wildcard         = "*"
	// maxIDLength bounds a configured or argument identifier. Excalidraw+ documents its collection and scene
	// identifiers only as strings, so this length and the character set of validID are a local, deliberately
	// narrow choice that keeps an identifier one opaque path or query value, never a separator or a URL.
	maxIDLength = 64
)

// scope is the boundary of one connection: every collection the key can see (an explicit wildcard) or an
// allow-list of collections and single scenes. A listed scene does not open its collection.
type scope struct {
	wildcard    bool
	collections []string
	scenes      []string
}

// sceneOnly reports an allow-list of scenes without any collection: nothing at collection level is part of it.
func (s scope) sceneOnly() bool { return !s.wildcard && len(s.collections) == 0 && len(s.scenes) > 0 }

func (s scope) listsScene(sceneID string) bool {
	for _, listed := range s.scenes {
		if listed == sceneID {
			return true
		}
	}
	return false
}

// allowsScene is the check of a scene read with its own collection: the collection is allowed or the scene
// itself is listed.
func (s scope) allowsScene(sceneID, collectionID string) bool {
	return s.allows(collectionID) || s.listsScene(sceneID)
}

// mayReachScene is the local check before any request: with no collection or wildcard only a listed scene can
// belong to the connection, so any other scene needs no proof request.
func (s scope) mayReachScene(sceneID string) bool {
	return s.wildcard || len(s.collections) > 0 || s.listsScene(sceneID)
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

// parseScope reads the configured targets: the single wildcard, or one or more collection/COLLECTION_ID and
// scene/SCENE_ID entries, none named twice. The wildcard already covers every scene, so it may not be combined
// with a scene target. No error quotes a configured value.
func parseScope(values []string) (scope, error) {
	if len(values) == 0 {
		return scope{}, errors.New("an Excalidraw+ connection needs at least one collection/COLLECTION_ID or " +
			"scene/SCENE_ID target or the * wildcard")
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
		kind, id, err := parseTarget(raw)
		if err != nil {
			return scope{}, err
		}
		if seen[kind+id] {
			return scope{}, errors.New("the Excalidraw+ allow-list names a " + kind + " more than once")
		}
		seen[kind+id] = true
		if kind == "scene" {
			bound.scenes = append(bound.scenes, id)
		} else {
			bound.collections = append(bound.collections, id)
		}
	}
	return bound, nil
}

// parseTarget reads one collection/COLLECTION_ID or scene/SCENE_ID entry.
func parseTarget(raw string) (kind, id string, err error) {
	const form = "an Excalidraw+ target must be collection/COLLECTION_ID, scene/SCENE_ID, or *"
	switch {
	case strings.HasPrefix(raw, collectionPrefix):
		kind, id = "collection", strings.TrimPrefix(raw, collectionPrefix)
	case strings.HasPrefix(raw, scenePrefix):
		kind, id = "scene", strings.TrimPrefix(raw, scenePrefix)
	default:
		return "", "", errors.New(form)
	}
	if !validID(id) {
		return "", "", errors.New(form)
	}
	return kind, id, nil
}

func validateTarget(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == wildcard {
		return nil
	}
	_, _, err := parseTarget(raw)
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
	values := provider.TargetsOf(resolved)
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
	if bound.sceneOnly() {
		return errSceneOnly
	}
	if !bound.allows(collectionID) {
		return invalidRequest("collection_id is outside the targets of this connection")
	}
	return nil
}

var errSceneOnly = invalidRequest("a connection bound to scenes only cannot use this tool, which is not part of a scene")

// requireCollections refuses, before any secret is read, a tool of collection or workspace level to a
// connection bound to scenes only.
func requireCollections(bound scope) error {
	if bound.sceneOnly() {
		return errSceneOnly
	}
	return nil
}

// selectScene checks a scene_id argument before any secret is resolved and before any request is sent. A scene
// outside a scene-only binding is refused here; otherwise the scene is proven afterwards by its own metadata.
// The refusal never names the scene.
func selectScene(resolved *config.Resolved, sceneID string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !validID(sceneID) {
		return invalidRequest("scene_id is not a valid identifier")
	}
	if !bound.mayReachScene(sceneID) {
		return invalidRequest("scene_id is outside the targets of this connection")
	}
	return nil
}

// reaches tells the tools a binding to single scenes only cannot serve: those on collections and on the
// workspace. Every other binding reaches every tool; the checks at the call stay in force.
func reaches(tool string, targets []string) (bool, []string) {
	var bound scope
	for _, raw := range targets {
		switch kind, id, err := parseTarget(strings.TrimSpace(raw)); {
		case err != nil && strings.TrimSpace(raw) == wildcard:
			bound.wildcard = true
		case err != nil:
		case kind == "scene":
			bound.scenes = append(bound.scenes, id)
		default:
			bound.collections = append(bound.collections, id)
		}
	}
	if !bound.sceneOnly() {
		return true, nil
	}
	switch tool {
	case collectionsList.ID, scenesList.ID, scenesCreate.ID, collectionsDelete.ID:
		return false, []string{"collection/COLLECTION_ID", wildcard}
	case logsList.ID, usersList.ID, usersGet.ID, usersUpdate.ID, usersRemove.ID:
		return false, []string{wildcard}
	}
	return true, nil
}

package twentycrm

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

// metaObjectExcluded reports whether an object read for a change is excluded on this connection. An object
// without a name cannot be matched and counts as excluded when the connection excludes anything.
func (c *Client) metaObjectExcluded(object *metaObjectRecord) bool {
	if len(c.scope.excluded) == 0 {
		return false
	}
	return object.NameSingular == nil || c.scope.excludes(*object.NameSingular)
}

// refuseExcludedObjects reads each object by identifier and refuses when one of them is excluded. Nothing is
// read on a connection without exclusions. The name comes from the read object and never from the arguments.
func (c *Client) refuseExcludedObjects(ctx context.Context, op string, ids ...string) error {
	if len(c.scope.excluded) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !validUUID(id) {
			return invalidRequest(errObjectUnavailable)
		}
		key := strings.ToLower(id)
		if seen[key] {
			continue
		}
		seen[key] = true
		var object metaObjectRecord
		if err := c.metaRead(ctx, op, metadataObjectsPath+"/"+url.PathEscape(id), "object", &object); err != nil {
			return err
		}
		if !equalUUID(object.ID, id) || c.metaObjectExcluded(&object) {
			return invalidRequest(errObjectUnavailable)
		}
	}
	return nil
}

type metaRelationObject struct {
	ID           string  `json:"id"`
	NameSingular *string `json:"nameSingular"`
}

type metaRelationEnds struct {
	Source *metaRelationObject `json:"sourceObjectMetadata"`
	Target *metaRelationObject `json:"targetObjectMetadata"`
}

// refuseExcludedField refuses a change to a field that belongs to an excluded object or whose relation
// reaches one. The owner and the relation ends are resolved by reading the objects; a relation field whose
// ends cannot be read is refused, because the read cannot prove it free of excluded objects.
func (c *Client) refuseExcludedField(ctx context.Context, op string, field *metaFieldRecord) error {
	if len(c.scope.excluded) == 0 {
		return nil
	}
	if field.ObjectMetadataID == nil {
		return invalidRequest(errObjectUnavailable)
	}
	ids := []string{*field.ObjectMetadataID}
	if field.Type == "RELATION" || field.Type == "MORPH_RELATION" {
		var ends []metaRelationEnds
		if raw := strings.TrimSpace(string(field.Relation)); raw != "" && raw != "null" {
			var one metaRelationEnds
			if json.Unmarshal(field.Relation, &one) != nil {
				return invalidRequest(errObjectUnavailable)
			}
			ends = append(ends, one)
		}
		if raw := strings.TrimSpace(string(field.MorphRelations)); raw != "" && raw != "null" {
			var many []metaRelationEnds
			if json.Unmarshal(field.MorphRelations, &many) != nil {
				return invalidRequest(errObjectUnavailable)
			}
			ends = append(ends, many...)
		}
		if len(ends) == 0 {
			return invalidRequest(errObjectUnavailable)
		}
		for _, end := range ends {
			for _, side := range []*metaRelationObject{end.Source, end.Target} {
				if side == nil {
					continue
				}
				if side.NameSingular != nil && c.scope.excludes(*side.NameSingular) {
					return invalidRequest(errObjectUnavailable)
				}
				if side.ID != "" {
					ids = append(ids, side.ID)
				}
			}
			if end.Source == nil && end.Target == nil {
				return invalidRequest(errObjectUnavailable)
			}
		}
	}
	return c.refuseExcludedObjects(ctx, op, ids...)
}

package bookstack

// Field lists of the instance-wide lists. Where a list sorts by more than it filters, the controller of
// BookStack gives the extra fields only as sort fields.
var (
	userListSchema = listSchema{
		Sort: []listField{
			{"id", kindID}, {"name", kindText}, {"slug", kindText}, {"email", kindText}, {"external_auth_id", kindText},
			{"created_at", kindTime}, {"updated_at", kindTime}, {"last_activity_at", kindTime},
		},
		Filter: []listField{
			{"id", kindID}, {"name", kindText}, {"slug", kindText}, {"email", kindText}, {"external_auth_id", kindText},
			{"updated_at", kindTime},
		},
	}
	// Roles are sorted by +id when no sort is given, although id is no listed field and so no sort value.
	roleListSchema = listSchema{
		Sort: []listField{
			{"display_name", kindText}, {"description", kindText}, {"mfa_enforced", kindBool},
			{"external_auth_id", kindText}, {"created_at", kindTime}, {"updated_at", kindTime},
			{"users_count", kindNumber}, {"permissions_count", kindNumber},
		},
		Filter: []listField{
			{"display_name", kindText}, {"description", kindText}, {"mfa_enforced", kindBool},
			{"external_auth_id", kindText}, {"created_at", kindTime}, {"updated_at", kindTime},
		},
	}
	auditLogListSchema = sameFields([]listField{
		{"id", kindID}, {"type", kindText}, {"detail", kindText}, {"user_id", kindID}, {"loggable_type", kindText},
		{"loggable_id", kindID}, {"ip", kindText}, {"created_at", kindTime},
	})
	recycleBinListSchema = sameFields([]listField{
		{"id", kindID}, {"deleted_by", kindID}, {"deletable_type", kindText}, {"deletable_id", kindID},
		{"created_at", kindTime}, {"updated_at", kindTime},
	})
	importListSchema = sameFields([]listField{
		{"id", kindID}, {"name", kindText}, {"size", kindNumber}, {"type", kindText}, {"created_by", kindID},
		{"created_at", kindTime}, {"updated_at", kindTime},
	})
)

// accountValue reads a filter field from a raw user row.
func accountValue(u accountJSON, field string) (any, bool) {
	switch field {
	case "id":
		return u.ID, true
	case "name":
		return u.Name, true
	case "slug":
		return u.Slug, true
	case "email":
		return u.Email, true
	case "external_auth_id":
		return u.ExternalAuthID, true
	case "updated_at":
		return u.UpdatedAt, true
	}
	return nil, false
}

// roleValue reads a filter field from a raw role row.
func roleValue(r roleJSON, field string) (any, bool) {
	switch field {
	case "display_name":
		return r.DisplayName, true
	case "description":
		return r.Description, true
	case "mfa_enforced":
		return r.MFAEnforced, true
	case "external_auth_id":
		return r.ExternalAuthID, true
	case "created_at":
		return r.CreatedAt, true
	case "updated_at":
		return r.UpdatedAt, true
	}
	return nil, false
}

// optionalID reads a nullable identifier; a row without one fails every filter on it.
func optionalID(id *int64) (any, bool) {
	if id == nil {
		return nil, false
	}
	return *id, true
}

// auditEntryValue reads a filter field from a raw audit log row.
func auditEntryValue(e auditEntryJSON, field string) (any, bool) {
	switch field {
	case "id":
		return e.ID, true
	case "type":
		return e.Type, true
	case "detail":
		return e.Detail, true
	case "user_id":
		return optionalID(e.UserID)
	case "loggable_type":
		return e.LoggableType, true
	case "loggable_id":
		return optionalID(e.LoggableID)
	case "ip":
		return e.IP, true
	case "created_at":
		return e.CreatedAt, true
	}
	return nil, false
}

// recycleBinValue reads a filter field from a raw recycle bin row.
func recycleBinValue(e recycleBinEntryJSON, field string) (any, bool) {
	switch field {
	case "id":
		return e.ID, true
	case "deleted_by":
		return e.DeletedBy, true
	case "deletable_type":
		return e.DeletableType, true
	case "deletable_id":
		return e.DeletableID, true
	case "created_at":
		return e.CreatedAt, true
	case "updated_at":
		return e.UpdatedAt, true
	}
	return nil, false
}

// importValue reads a filter field from a raw import row.
func importValue(i importJSON, field string) (any, bool) {
	switch field {
	case "id":
		return i.ID, true
	case "name":
		return i.Name, true
	case "size":
		return i.Size, true
	case "type":
		return i.Type, true
	case "created_by":
		return i.CreatedBy, true
	case "created_at":
		return i.CreatedAt, true
	case "updated_at":
		return i.UpdatedAt, true
	}
	return nil, false
}

package seatable

// The table boundary of a connection is decided here and nowhere else. Every tool asks these helpers
// instead of reading the scope fields, so a rule about which tables a connection reaches changes in one place.

// wholeBase reports whether the connection reaches every table of the base without any restriction. Code
// that skips a boundary check for the whole base asks this, never the wildcard flag itself.
func (s scope) wholeBase() bool {
	return s.wildcard
}

// admits reports whether a selected target is inside the scope by its text alone, before any metadata is read.
func (s scope) admits(selected target) bool {
	if s.wholeBase() {
		return true
	}
	for _, allowed := range s.targets {
		if selected == allowed {
			return true
		}
	}
	return false
}

// reaches reports whether the connection may read the table.
func (s scope) reaches(table tableJSON) bool {
	if s.wholeBase() {
		return true
	}
	for _, allowed := range s.targets {
		if matchesTable(allowed, table) {
			return true
		}
	}
	return false
}

// reference returns the invocation reference of a table the connection reaches. A restricted connection
// hands out the form its own target uses.
func (s scope) reference(table tableJSON) (string, bool) {
	if s.wholeBase() {
		return idPrefix + table.ID, true
	}
	for _, wanted := range s.targets {
		if matchesTable(wanted, table) {
			return formatTarget(wanted), true
		}
	}
	return "", false
}

// namedTarget reports whether a restricted connection holds the table as a target by its name.
func (s scope) namedTarget(table tableJSON) bool {
	if s.wholeBase() {
		return false
	}
	for _, configured := range s.targets {
		if configured.tableParam == "table_name" && matchesTable(configured, table) {
			return true
		}
	}
	return false
}

// namedViewTarget reports whether a view of the table is configured as a target of the connection.
func (s scope) namedViewTarget(table tableJSON, id, name string) bool {
	for _, configured := range s.targets {
		if configured.view == "" || !matchesTable(configured, table) {
			continue
		}
		if (configured.viewParam == "view_id" && configured.view == id) ||
			(configured.viewParam == "view_name" && configured.view == name) {
			return true
		}
	}
	return false
}

// findTable returns the table of the metadata a selected target names, or nil when the base holds none.
func findTable(document metadataJSON, selected target) *tableJSON {
	for i := range document.Metadata.Tables {
		if matchesTable(selected, document.Metadata.Tables[i]) {
			return &document.Metadata.Tables[i]
		}
	}
	return nil
}

// tableAllowed reports whether the table with this identifier is inside the connection's allow-list.
func (c *Client) tableAllowed(document metadataJSON, id string) bool {
	for _, table := range document.Metadata.Tables {
		if table.ID == id && c.scope.reaches(table) {
			return true
		}
	}
	return false
}

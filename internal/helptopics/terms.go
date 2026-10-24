package helptopics

// Term is the short explanation of one technical word. ID is the stable key an editor or a manpage refers to
// (term.<word>); it never changes once shipped. Text is one to three plain ASCII sentences.
type Term struct {
	ID    string
	Title string
	Text  string
}

// Terms returns the terms in the order a newcomer meets them.
func Terms() []Term {
	return []Term{
		{ID: "term.service", Title: "Service", Text: "A service is one instance of a system, such as one wiki or one " +
			"issue tracker: a provider and the root URL it is reached at. Several connections can share a service."},
		{ID: "term.credential", Title: "Credential", Text: "A credential says where the secrets for a service come " +
			"from, such as the system keyring, the vault, or environment variables. It never contains the secrets " +
			"themselves in the configuration."},
		{ID: "term.connection", Title: "Connection", Text: "A connection is the route an agent takes: one service " +
			"with one credential, an optional target, and the effects and tools it may use. Agents see only its " +
			"name and description."},
		{ID: "term.target", Title: "Target", Text: "A target narrows a connection to a part of its service, such as " +
			"one project or one space. A connection without a target reaches whatever the credential reaches."},
		{ID: "term.effect", Title: "Effect", Text: "An effect is what a tool does to data: read, create, update, " +
			"delete, or execute. A connection permits effects, and only tools with a permitted effect are offered."},
		{ID: "term.profile", Title: "Profile", Text: "A profile is a named starting selection of a provider's " +
			"tools. Choosing one ticks its tools once; only the ticks are saved, not the profile."},
		{ID: "term.approval", Title: "Approval", Text: "With an encrypted vault, a connection that reads a vault " +
			"secret works only after a person approved it as it is configured now. Changing it asks for a new " +
			"approval."},
		{ID: "term.token", Title: "Token", Text: "An agent token lets an agent approve connection changes itself, " +
			"within the limits of the vorbild connections it names. It is shown once and kept in the vault."},
		{ID: "term.vault", Title: "Vault", Text: "The vault is the encrypted store for secrets, unlocked with a " +
			"passphrase. Reading needs no passphrase while a vault process holds it unlocked; managing it needs an " +
			"admin session."},
	}
}

// LookupTerm returns the term with the given ID.
func LookupTerm(id string) (Term, bool) {
	for _, t := range Terms() {
		if t.ID == id {
			return t, true
		}
	}
	return Term{}, false
}

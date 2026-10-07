package web

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// connectionTestTimeout bounds how long the result page's "Test this connection" button waits for an
// answer, the same way every other provider call this build makes is bounded: a request never blocks the
// coupled browser forever on a provider that never answers.
const connectionTestTimeout = 30 * time.Second

// handleTestConnection runs this run's injected Tester (see New) for one saved connection, over the same
// path internal/tui's own Connections section runs it: cfg.Resolve, the run's own resolver, and
// Registry.TestConnection, all inside the Tester this package never builds itself (see
// internal/cli/web.go's own connectionTester). It writes nothing, so it needs only withSessionGuard, not
// withAdminGuard: an encrypted vault's admin approval protects what a mutation may do, not a read-only
// probe of a connection already saved. No retry is automatic; a person presses the button again for that.
func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	if !s.credentialsReady(w) {
		return
	}
	name := r.PathValue("name")
	cfg, err := s.loadConfig()
	if err != nil {
		http.Error(w, s.redact(err.Error()), http.StatusInternalServerError)
		return
	}
	conn, ok := cfg.Connections[name]
	if !ok {
		http.Error(w, "unknown connection", http.StatusNotFound)
		return
	}
	if s.tester == nil {
		s.renderConnectionResult(w, cfg, name, conn, "", "connection testing is not available for this run")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), connectionTestTimeout)
	defer cancel()
	class, err := s.tester(ctx, name)
	providerName := name
	if metadata, found := cfg.ProviderMetadata(cfg.Services[conn.Service].Provider); found {
		providerName = metadata.Name
	}
	s.renderConnectionResult(w, cfg, name, conn, "", testResultText(class, err, providerName, s.redact))
}

// testResultText turns a Tester's own outcome into the one line the result page shows, redacted exactly
// the way every other error this package displays is (see s.redact): never a secret, whatever the provider
// answered with. It reads the same stable provider.Class internal/tui's own testLine does, in its own
// words, since neither package shares a rendering layer with the other.
func testResultText(class provider.Class, err error, providerName string, redact func(string) string) string {
	if err != nil {
		return "Connection test could not run: " + redact(err.Error())
	}
	if class == provider.ClassOK {
		return fmt.Sprintf("Test succeeded: %s accepted the connection.", providerName)
	}
	explanation, ok := testClassExplanation[class]
	if !ok {
		explanation = "see the class for what to check"
	}
	return fmt.Sprintf("Test failed (%s): %s", class, explanation)
}

// testClassExplanation mirrors internal/tui's own testLine explanation map, in fewer words: what a person
// checks next for each stable outcome a connection test can report.
var testClassExplanation = map[provider.Class]string{
	provider.ClassUnreachable:     "the server did not answer; check the base URL and network",
	provider.ClassTLS:             "the secure connection failed; check the server certificate and URL",
	provider.ClassAuth:            "the provider rejected the credential or it lacks permission",
	provider.ClassPermission:      "the provider accepted the credential but refused the operation",
	provider.ClassTimeout:         "the provider did not answer before the request deadline",
	provider.ClassRateLimited:     "the provider is rate-limiting requests; wait and try again",
	provider.ClassInvalidResponse: "the provider returned an invalid response",
	provider.ClassProviderError:   "the provider returned an unusable response; check the root URL and API access",
	provider.ClassNotFound:        "the provider did not find the target; check the target and whether the credential may see it",
}

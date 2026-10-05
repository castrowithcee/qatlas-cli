package releasetest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Server is a local stand-in for the GitHub releases URL of a repository, as the installation scripts
// use it: /latest redirects to /tag/<tag>, and /download/<tag>/<name> serves a release asset.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	releases map[string]map[string][]byte
	latest   string
}

// NewServer starts an empty release server that is closed when the test ends. Its URL is the releases
// base URL.
func NewServer(t testing.TB) *Server {
	t.Helper()
	s := &Server{releases: map[string]map[string][]byte{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Server.Close)
	return s
}

// Publish adds a release with the given assets, keyed by file name, and makes it the latest release.
func (s *Server) Publish(tag string, assets map[string][]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases[tag] = assets
	s.latest = tag
}

// SetLatest points /latest at an already published tag.
func (s *Server) SetLatest(tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = tag
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case r.URL.Path == "/latest" && s.latest != "":
		http.Redirect(w, r, "/tag/"+s.latest, http.StatusFound)
	case strings.HasPrefix(r.URL.Path, "/tag/") && s.releases[strings.TrimPrefix(r.URL.Path, "/tag/")] != nil:
		w.WriteHeader(http.StatusOK)
	case strings.HasPrefix(r.URL.Path, "/download/"):
		tag, name, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/download/"), "/")
		data, found := s.releases[tag][name]
		if !ok || !found {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

package selfupdate

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStallTimer counts resets and fires only when the test says so.
type fakeStallTimer struct {
	mu     sync.Mutex
	f      func()
	resets int
	reset  chan struct{}
}

func (f *fakeStallTimer) Reset(time.Duration) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resets++
	select {
	case f.reset <- struct{}{}:
	default:
	}
	return true
}

func (f *fakeStallTimer) Stop() bool { return true }

func (f *fakeStallTimer) Resets() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resets
}

func (f *fakeStallTimer) Fire() { f.f() }

// useFakeStallTimer installs a fake watchdog; the returned channel yields it once request creates it.
func useFakeStallTimer(t *testing.T) <-chan *fakeStallTimer {
	t.Helper()
	saved := newStallTimer
	created := make(chan *fakeStallTimer, 1)
	newStallTimer = func(_ time.Duration, f func()) stallTimer {
		timer := &fakeStallTimer{f: f, reset: make(chan struct{}, 1)}
		created <- timer
		return timer
	}
	t.Cleanup(func() { newStallTimer = saved })
	return created
}

// A download that keeps moving is never cut off: every block resets the watchdog and progress rises.
func TestRequestOutlastsTheStallTimeoutWhileDataFlows(t *testing.T) {
	created := useFakeStallTimer(t)
	chunk := bytes.Repeat([]byte("x"), 100)
	const chunks = 8
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(chunks*len(chunk)))
		for i := 0; i < chunks; i++ {
			_, _ = w.Write(chunk)
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()
	client := &Client{HTTPClient: server.Client(), Version: "v1.0.0"}
	var dones []int64
	var totals []int64
	body, err := client.request(context.Background(), server.URL, 1<<20, "application/octet-stream",
		func(done, total int64) { dones = append(dones, done); totals = append(totals, total) })
	if err != nil {
		t.Fatalf("request() = %v, want the whole download", err)
	}
	if resets := (<-created).Resets(); resets < len(dones) || len(dones) < 1 {
		t.Fatalf("watchdog reset %d times for %d progress reports, want one per block", resets, len(dones))
	}
	if len(body) != chunks*len(chunk) {
		t.Fatalf("body has %d bytes, want %d", len(body), chunks*len(chunk))
	}
	if len(dones) == 0 || dones[len(dones)-1] != int64(len(body)) {
		t.Fatalf("progress reported %v, want it to end at %d", dones, len(body))
	}
	for i := range dones {
		if totals[i] != int64(len(body)) || (i > 0 && dones[i] <= dones[i-1]) {
			t.Fatalf("progress %v of %v, want rising counts of the announced total", dones, totals)
		}
	}
}

// A download that stops sending ends with the stall named, not with a bare cancellation.
func TestRequestEndsAStalledDownload(t *testing.T) {
	created := useFakeStallTimer(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client := &Client{HTTPClient: server.Client(), Version: "v1.0.0"}
	// Fire only after the partial block reset the watchdog, so the stall comes mid-download.
	go func() {
		timer := <-created
		<-timer.reset
		timer.Fire()
	}()
	_, err := client.request(context.Background(), server.URL, 1<<20, "application/octet-stream", nil)
	if err == nil || !strings.Contains(err.Error(), "no data received") {
		t.Fatalf("request() = %v, want the stall reported", err)
	}
}

// Waiting for an answer that never starts counts as a stall too.
func TestRequestEndsWhenTheAnswerNeverStarts(t *testing.T) {
	created := useFakeStallTimer(t)
	arrived := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-r.Context().Done()
	}))
	defer server.Close()
	client := &Client{HTTPClient: server.Client(), Version: "v1.0.0"}
	go func() {
		timer := <-created
		<-arrived
		timer.Fire()
	}()
	_, err := client.request(context.Background(), server.URL, 1<<20, "application/octet-stream", nil)
	if err == nil || !strings.Contains(err.Error(), "no data received") {
		t.Fatalf("request() = %v, want the stall reported", err)
	}
}

// Update reports the progress of the archive download only.
func TestUpdateReportsArchiveProgress(t *testing.T) {
	archive := releaseArchive(t, []byte("new-binary"), []byte("new-manpage"))
	server := releaseServer(t, "v1.1.0", archive, "")
	defer server.Close()
	executable := filepath.Join(t.TempDir(), "bin", "qatlas")
	if err := os.MkdirAll(filepath.Dir(executable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var last int64
	client := &Client{
		BaseURL: server.URL, HTTPClient: server.Client(), Version: "v1.0.0",
		GOOS: "linux", GOARCH: "amd64", Executable: executable,
		Progress: func(done, total int64) { last = done },
	}
	if _, err := client.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if last != int64(len(archive)) {
		t.Fatalf("last progress = %d, want the archive's %d bytes", last, len(archive))
	}
}

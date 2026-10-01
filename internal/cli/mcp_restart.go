package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"runtime"
	"strings"
	"unicode"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

const (
	// mcpHandoffEnv carries the state of a session from a server that restarts itself to its successor.
	// The successor removes it from its environment as soon as it has read it, so no child inherits it.
	mcpHandoffEnv     = "QATLAS_MCP_HANDOFF"
	mcpHandoffVersion = 1
	// maxMCPHandoffBytes bounds the encoded handoff on both sides. A single environment string cannot
	// hold much more than 128 KiB, and the value of a stranger is no more trusted to stay small than
	// client data is.
	maxMCPHandoffBytes = 96 << 10
	maxMCPHandoffField = 1024
)

// mcpRestart is how a server restarts itself after an update replaced its program. A zero value disables
// it; replaced and exec are fields so a test reaches every outcome without a second program.
type mcpRestart struct {
	// replaced returns the path the running program started from and whether its file was replaced since.
	replaced func() (string, bool)
	// exec replaces the process image, and returns only when it failed.
	exec func(path string, argv, env []string) error
	// tried is set once a restart was attempted, so a failed one is not repeated with every message.
	tried bool
	// skipCheck leaves the next message unchecked in a server that has just restarted, so a program that
	// still counts as replaced cannot restart again without input.
	skipCheck bool
}

// mcpHandoff is what a restarting server passes on: what the client negotiated and what it said about its
// roots, and the bytes already read from stdin that were not handled yet. It carries no secret.
type mcpHandoff struct {
	Version  int                   `json:"v"`
	From     string                `json:"from"` // version of the program that restarted
	Protocol string                `json:"protocol,omitempty"`
	Client   *invokelog.ClientInfo `json:"client,omitempty"`
	Roots    mcpHandoffRoots       `json:"roots"`
	Input    []byte                `json:"input,omitempty"`
}

type mcpHandoffRoots struct {
	Offered     bool     `json:"offered,omitempty"`
	ListChanged bool     `json:"listChanged,omitempty"`
	Sequence    int      `json:"sequence,omitempty"`
	Ask         bool     `json:"ask,omitempty"` // roots/list is asked again: an answer was open or the dirs did not fit
	Dirs        []string `json:"dirs,omitempty"`
}

// takeMCPHandoff reads the handoff a predecessor left in the environment and removes it from there. It
// returns nil where there is none, an unknown version, or anything malformed: the server then starts as
// without a handoff.
func takeMCPHandoff() *mcpHandoff {
	value, ok := os.LookupEnv(mcpHandoffEnv)
	if !ok {
		return nil
	}
	_ = os.Unsetenv(mcpHandoffEnv)
	return decodeMCPHandoff(value)
}

func decodeMCPHandoff(value string) *mcpHandoff {
	if len(value) > maxMCPHandoffBytes {
		return nil
	}
	var handoff mcpHandoff
	if json.Unmarshal([]byte(value), &handoff) != nil || handoff.Version != mcpHandoffVersion {
		return nil
	}
	if len(handoff.From) > maxMCPHandoffField || strings.ContainsFunc(handoff.From, unicode.IsControl) ||
		len(handoff.Input) > maxMCPHandoffBytes {
		return nil
	}
	if handoff.Protocol != "" && !isMCPLegacyVersion(handoff.Protocol) {
		return nil
	}
	if handoff.Client != nil {
		client := handoff.Client
		if client.Name == "" || client.Version == "" || len(client.Name) > maxMCPHandoffField ||
			len(client.Version) > maxMCPHandoffField {
			return nil
		}
	}
	roots := handoff.Roots
	if roots.Sequence < 0 || roots.Sequence > 1<<30 || len(roots.Dirs) > maxMCPRoots {
		return nil
	}
	for _, dir := range roots.Dirs {
		// A directory has the form fileURIPath gives it. Windows restarts nothing, so no directory is
		// accepted there.
		if runtime.GOOS == "windows" || len(dir) > maxMCPRootURIBytes || !strings.HasPrefix(dir, "/") ||
			strings.ContainsRune(dir, 0) || dir != path.Clean(dir) {
			return nil
		}
	}
	return &handoff
}

func isMCPLegacyVersion(version string) bool {
	for _, known := range mcpLegacyVersions {
		if version == known {
			return true
		}
	}
	return false
}

// adopt takes the state of a predecessor. Roots whose request was still open are asked for again once
// the loop runs.
func (s *mcpServer) adopt(handoff *mcpHandoff) {
	s.legacy = handoff.Protocol
	s.legacyClient = handoff.Client
	s.restart.skipCheck = true
	s.restartFrom = handoff.From
	roots := handoff.Roots
	s.rootsMu.Lock()
	defer s.rootsMu.Unlock()
	s.roots = mcpRoots{offered: roots.Offered, listChanged: roots.ListChanged, sequence: roots.Sequence}
	if roots.Ask {
		s.resumeRoots = true
		return
	}
	done := &mcpRootsRound{done: make(chan struct{})}
	done.finish()
	s.roots.round, s.roots.dirs = done, roots.Dirs
}

// resume starts a restarted server: it says which versions it runs between and asks again for roots.
func (s *mcpServer) resume() {
	if s.restartFrom == "" {
		return
	}
	s.errMu.Lock()
	fmt.Fprintf(s.stderr, "qatlas: mcp: restarted after an update, version %s to %s\n", s.restartFrom, s.version)
	s.errMu.Unlock()
	if s.resumeRoots {
		s.requestRoots(false)
	}
}

// restartIfReplaced restarts the server in the same process when an update replaced its program, before
// the line just read is handled. It lets the running calls finish and write their answers first, then
// executes the program at its original path with the same arguments and the session in the environment;
// the line and what is still buffered are handed over unhandled. It returns only where nothing was
// restarted: the program is not replaced, a restart was tried before, or exec failed. The server then runs
// on as before.
func (s *mcpServer) restartIfReplaced(reader *bufio.Reader, line []byte) {
	r := &s.restart
	if r.replaced == nil || r.exec == nil || r.tried {
		return
	}
	if r.skipCheck {
		r.skipCheck = false
		return
	}
	path, replaced := r.replaced()
	if !replaced {
		return
	}
	// The new program must be there before anything waits for it; otherwise nothing changes.
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		r.tried = true
		s.restartNote("no executable program at %s, not restarting", path)
		return
	}
	buffered, _ := reader.Peek(reader.Buffered())
	input := make([]byte, 0, len(line)+1+len(buffered))
	input = append(append(append(input, line...), '\n'), buffered...)
	if len(input) > maxMCPHandoffBytes/2 {
		return // too much unhandled input to hand over; the next line is checked again
	}

	s.wg.Wait()
	s.outMu.Lock()
	failed := s.writeErr != nil
	s.outMu.Unlock()
	if failed {
		return
	}
	r.tried = true

	handoff := mcpHandoff{Version: mcpHandoffVersion, From: s.version, Protocol: s.legacy, Client: s.legacyClient,
		Input: input}
	s.rootsMu.Lock()
	handoff.Roots = mcpHandoffRoots{Offered: s.roots.offered, ListChanged: s.roots.listChanged,
		Sequence: s.roots.sequence, Ask: s.roots.pending != "", Dirs: s.roots.dirs}
	s.rootsMu.Unlock()
	encoded, err := json.Marshal(handoff)
	if err == nil && len(encoded) > maxMCPHandoffBytes {
		// The roots do not fit: the successor asks for them again.
		handoff.Roots.Ask, handoff.Roots.Dirs = true, nil
		encoded, err = json.Marshal(handoff)
	}
	if err != nil || len(encoded) > maxMCPHandoffBytes {
		s.restartNote("cannot hand over the session, not restarting")
		return
	}

	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, mcpHandoffEnv+"=") {
			env = append(env, entry)
		}
	}
	env = append(env, mcpHandoffEnv+"="+string(encoded))
	s.restartNote("program replaced, restarting from version %s", s.version)
	if err := r.exec(path, os.Args, env); err != nil {
		s.restartNote("restart failed, running on: %v", err)
	}
}

func (s *mcpServer) restartNote(format string, args ...any) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	fmt.Fprintf(s.stderr, "qatlas: mcp: "+format+"\n", args...)
}

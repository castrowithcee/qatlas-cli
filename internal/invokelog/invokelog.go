// Package invokelog appends one entry per CLI or MCP invoke to a daily file under the vault directory and
// links entries with a SHA-256 hash chain that spans day boundaries: the first entry of a day carries the
// hash of the last entry of the last day that has one, so the chain keeps going across a day nobody
// invoked anything on and a day file never has to exist just to carry the link.
//
// A day file is vault/logs/YYYY-MM-DD.jsonl, named by its UTC date; every entry it holds is one JSON
// object per line. Append takes an exclusive lock on the logs directory for its whole read-compute-write,
// so concurrent writers, goroutines or processes, never race the chain. A failed Append is reported to the
// caller, never written silently and never left to abort the invoke it was called for.
//
// Nothing here reads or writes a check value bound to the vault's key: Entry.MAC stays empty until an entry
// is signed, which marks it unverified. Verify already treats an empty MAC as "unverified", never as a
// break, so signing can be added later without changing this format.
package invokelog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

const (
	dirMode      = 0o700
	fileMode     = 0o600
	logsDirName  = "logs"
	lockFileName = ".lock"
)

// dayFilePattern matches a day file's name and captures its date.
var dayFilePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.jsonl$`)

// genesisHash is the prev_hash of the very first entry this installation ever logs: the SHA-256 of zero
// bytes, the canonical hash of "nothing came before this".
var genesisHash = hashHex(nil)

// KindCut marks a retention-cut marker entry (see Entry.Kind), as opposed to the empty Kind of an
// invocation entry.
const KindCut = "retention-cut"

// ClientInfo names the MCP client that made an invoke, from its initialize request or its per-request
// declaration; nil on the CLI and wherever a client never declared one.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Cut documents a retention deletion. Append writes it as the entry right after the deletion, naming the
// sequence number of the last entry the deletion removed, so Verify never mistakes a documented cut for a
// corrupted, undocumented gap.
type Cut struct {
	ThroughSeq uint64 `json:"through_seq"`
}

// Entry is one line of a log file, in the field order it is written. Kind is empty for an invocation and
// KindCut for a retention-cut marker, which carries only Cut beside the chain fields. MAC is the check
// value of a signed entry; it stays empty until the entry is signed, which marks it unverified. No field
// ever holds an argument, a result, a secret, a target, or a URL query.
type Entry struct {
	Seq        uint64      `json:"seq"`
	Time       time.Time   `json:"time"`
	Kind       string      `json:"kind,omitempty"`
	Path       string      `json:"path,omitempty"`
	Client     *ClientInfo `json:"client,omitempty"`
	Operation  string      `json:"operation,omitempty"`
	Version    int         `json:"version,omitempty"`
	Connection string      `json:"connection,omitempty"`
	Effect     string      `json:"effect,omitempty"`
	Result     string      `json:"result,omitempty"`
	DurationMS int64       `json:"duration_ms,omitempty"`
	Cut        *Cut        `json:"cut,omitempty"`
	PrevHash   string      `json:"prev_hash"`
	// MAC is the check value of a signed entry; empty until the entry is signed, on purpose.
	MAC string `json:"mac"`
}

// Fields is what a caller supplies for one completed invoke; Append computes Seq, Time, PrevHash, and MAC.
type Fields struct {
	// Path is "cli" or "mcp".
	Path       string
	Client     *ClientInfo
	Operation  string
	Version    int
	Connection string
	Effect     string
	// Result is "success" or the failure's error code, the same value application.auditResult uses.
	Result   string
	Duration time.Duration
}

// Logger appends invoke entries under one vault directory's logs subdirectory. The zero value is not
// usable; use New.
type Logger struct {
	vaultDir      string
	retentionDays int
	// now is the logger's clock; nil means time.Now. A test overrides it to move the clock across days
	// without waiting.
	now func() time.Time
}

// New returns a logger writing under vaultDir/logs. It touches no file: like the vault itself, the
// directory and its files are created lazily, on the first Append, and New never initializes a vault.
func New(vaultDir string, retentionDays int) *Logger {
	return &Logger{vaultDir: vaultDir, retentionDays: retentionDays}
}

func (l *Logger) logsDir() string { return filepath.Join(l.vaultDir, logsDirName) }

func (l *Logger) clock() time.Time {
	if l.now != nil {
		return l.now().UTC()
	}
	return time.Now().UTC()
}

// Append writes one entry for a completed invoke, chained to the last entry this installation ever wrote,
// across day files and across processes. Retention runs first, under the same lock: a day file older than
// the logger's retention window is removed, and if any file was actually removed, a KindCut marker
// documenting the cut is written immediately before the real entry, in the same file.
//
// A returned error means the entry might not be durable. The caller reports it as a warning; it must never
// turn a completed invoke into a failure or change its result.
func (l *Logger) Append(f Fields) error {
	dir := l.logsDir()
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return fmt.Errorf("lock %s: %w", dir, err)
	}
	defer unlock()

	now := l.clock()
	todayPath := filepath.Join(dir, now.Format("2006-01-02")+".jsonl")

	removed, removedRaw, err := applyRetention(dir, now, l.retentionDays)
	if err != nil {
		return err
	}
	last, lastRaw, err := lastLine(dir)
	if err != nil {
		return err
	}

	// A surviving file is always more recent than anything retention just removed, so it wins when both
	// exist; removedRaw is the fallback for the case retention just deleted every existing file, so the
	// chain still continues from what was removed instead of silently resetting to the genesis hash.
	prevHash, seq := genesisHash, uint64(1)
	switch {
	case lastRaw != nil:
		prevHash, seq = hashHex(lastRaw), last.Seq+1
	case removedRaw != nil:
		prevHash, seq = hashHex(removedRaw), removed.Seq+1
	}

	var lines [][]byte
	if removedRaw != nil {
		cut := Entry{Seq: seq, Time: now, Kind: KindCut, Cut: &Cut{ThroughSeq: removed.Seq}, PrevHash: prevHash}
		raw, err := json.Marshal(cut)
		if err != nil {
			return fmt.Errorf("encode retention cut entry: %w", err)
		}
		lines = append(lines, raw)
		prevHash, seq = hashHex(raw), seq+1
	}

	entry := Entry{
		Seq: seq, Time: now, Path: f.Path, Client: f.Client, Operation: f.Operation, Version: f.Version,
		Connection: f.Connection, Effect: f.Effect, Result: f.Result, DurationMS: f.Duration.Milliseconds(),
		PrevHash: prevHash,
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode invocation log entry: %w", err)
	}
	lines = append(lines, raw)

	return appendLines(todayPath, lines)
}

// applyRetention removes every day file older than retentionDays, relative to now, and returns the last
// entry among the files it removed and its exact raw line, or a nil line when nothing was old enough to
// remove. now and retentionDays come from the same Append call, so the cutoff and the deleted files agree
// with each other. Retention only ever removes a contiguous run of the oldest days, so the boundary it
// leaves behind is always the newest of exactly the files it just deleted.
func applyRetention(dir string, now time.Time, retentionDays int) (Entry, []byte, error) {
	files, err := dayFiles(dir)
	if err != nil {
		return Entry{}, nil, err
	}
	cutoff := now.AddDate(0, 0, -retentionDays).Format("2006-01-02")
	var expired []string
	for _, name := range files {
		if dayFilePattern.FindStringSubmatch(name)[1] < cutoff {
			expired = append(expired, name)
		}
	}
	if len(expired) == 0 {
		return Entry{}, nil, nil
	}
	// files is already sorted chronologically, and expired keeps that order: its last entry is the last
	// entry the deletion below removes.
	last, raw, err := lastLineOf(filepath.Join(dir, expired[len(expired)-1]))
	if err != nil {
		return Entry{}, nil, err
	}
	for _, name := range expired {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return Entry{}, nil, fmt.Errorf("remove expired log %s: %w", name, err)
		}
	}
	if raw == nil {
		// The expired file that was newest turned out empty, so it left nothing to document a cut through.
		return Entry{}, nil, nil
	}
	return last, raw, nil
}

// dayFiles returns the day file names of dir, sorted chronologically, which is also lexicographic for a
// YYYY-MM-DD name. A logs directory that does not exist yet holds none.
func dayFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && dayFilePattern.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// lastLine returns the last entry and its exact raw line among every day file of dir, most recent file
// first, and nil when none holds a line yet.
func lastLine(dir string) (Entry, []byte, error) {
	files, err := dayFiles(dir)
	if err != nil {
		return Entry{}, nil, err
	}
	for i := len(files) - 1; i >= 0; i-- {
		entry, raw, err := lastLineOf(filepath.Join(dir, files[i]))
		if err != nil {
			return Entry{}, nil, err
		}
		if raw != nil {
			return entry, raw, nil
		}
	}
	return Entry{}, nil, nil
}

// lastLineOf returns the last line of path and the entry it decodes to, or a nil line when the file does
// not exist or holds no line.
func lastLineOf(path string) (Entry, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Entry{}, nil, nil
		}
		return Entry{}, nil, fmt.Errorf("read %s: %w", path, err)
	}
	lines := splitLines(data)
	if len(lines) == 0 {
		return Entry{}, nil, nil
	}
	raw := lines[len(lines)-1]
	var entry Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return Entry{}, nil, fmt.Errorf("parse %s: not a valid log entry", path)
	}
	return entry, raw, nil
}

// splitLines returns the non-empty lines of data, without their trailing newline.
func splitLines(data []byte) [][]byte {
	data = bytes.TrimRight(data, "\n")
	if len(data) == 0 {
		return nil
	}
	return bytes.Split(data, []byte("\n"))
}

// appendLines appends every line of lines, each followed by a newline, to path, creating it at fileMode if
// it does not exist yet and forcing that mode either way, the same rule the vault package's own files
// follow. It flushes before returning, so a crash right after Append cannot leave a torn line.
func appendLines(path string, lines [][]byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if err := f.Chmod(fileMode); err != nil {
		return fmt.Errorf("set the permissions of %s: %w", path, err)
	}
	for _, line := range lines {
		if _, err := f.Write(append(append([]byte(nil), line...), '\n')); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// hashHex is the canonical hash of one line: the hex-encoded SHA-256 of its exact bytes, without a
// trailing newline. It is applied only to a line exactly as Append wrote it or Verify read it, never to a
// re-encoding, so a byte that changes anywhere in the line changes the hash.
func hashHex(line []byte) string {
	sum := sha256.Sum256(line)
	return hex.EncodeToString(sum[:])
}

// lockDir opens (creating if needed) and exclusively locks dir's lock file, and returns a function that
// unlocks and closes it. dir must already exist.
func lockDir(dir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, err
	}
	if err := lockExclusive(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = unlockExclusive(f)
		_ = f.Close()
	}, nil
}

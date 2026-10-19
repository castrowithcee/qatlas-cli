package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// writeStatsLog writes entries as day files named by their UTC date, with local time set to UTC so the
// local days of the test are the file names. The chain is not valid; stats does not check it.
func writeStatsLog(t *testing.T, dir string, entries []invokelog.Entry) {
	t.Helper()
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })
	logs := filepath.Join(dir, vault.DirName, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]*strings.Builder{}
	for i, e := range entries {
		e.Seq = uint64(i + 1)
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		day := e.Time.UTC().Format(invokelog.DateLayout)
		if files[day] == nil {
			files[day] = &strings.Builder{}
		}
		files[day].Write(raw)
		files[day].WriteByte('\n')
	}
	for day, content := range files {
		if err := os.WriteFile(filepath.Join(logs, day+".jsonl"), []byte(content.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func statsEntry(day, hour, op, conn string, resp *int64) invokelog.Entry {
	when, _ := time.Parse(time.RFC3339, day+"T"+hour+":00:00Z")
	return invokelog.Entry{Time: when, Path: "mcp", Operation: op, Connection: conn, Effect: "read",
		Result: "success", RespBytes: resp}
}

func statsFixture(t *testing.T) string {
	dir := vaultCredentialFixture(t)
	n := func(v int64) *int64 { return &v }
	writeStatsLog(t, dir, []invokelog.Entry{
		statsEntry("2026-05-01", "10", "wiki.pages.get", "wiki", n(3500)),
		statsEntry("2026-05-02", "10", "wiki.pages.get", "wiki", n(7000)),
		statsEntry("2026-05-02", "11", "wiki.pages.get", "wiki", nil),
		statsEntry("2026-05-02", "12", "chat.send", "alerts", n(0)),
		statsEntry("2026-05-03", "10", "wiki.pages.get", "wiki", n(70)),
		statsEntry("2026-05-03", "11", "old.tool", "wiki", nil),
		{Time: time.Date(2026, 5, 2, 9, 45, 0, 0, time.UTC), Path: "cli", Operation: invokelog.OperationLock, Result: "program-replaced"},
		{Time: time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC), Kind: invokelog.KindCut, Cut: &invokelog.Cut{ThroughSeq: 1}},
		{Time: time.Date(2026, 5, 2, 9, 30, 0, 0, time.UTC), Path: "tui", Operation: invokelog.OperationConnectionCreate,
			Connection: "wiki", Effect: "create", Result: "success"},
	})
	return dir
}

func TestVaultLogsStatsJSON(t *testing.T) {
	dir := statsFixture(t)
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "logs", "stats", "--config", configIn(dir),
		"--output", "json", "--from", "2026-05-02", "--to", "2026-05-02")
	want := `{"from":"2026-05-02","to":"2026-05-02","groups":[` +
		`{"operation":"wiki.pages.get","connection":"wiki","calls":2,"no_counter":1,"resp_bytes_total":7000,` +
		`"resp_bytes_max":7000,"resp_bytes_p50":7000,"resp_bytes_p95":7000,"est_tokens_total":2000,` +
		`"est_tokens_max":2000,"est_tokens_p50":2000,"est_tokens_p95":2000},` +
		`{"operation":"chat.send","connection":"alerts","calls":1,"no_counter":0,"resp_bytes_total":0,` +
		`"resp_bytes_max":0,"resp_bytes_p50":0,"resp_bytes_p95":0,"est_tokens_total":0,` +
		`"est_tokens_max":0,"est_tokens_p50":0,"est_tokens_p95":0}]}` + "\n"
	if code != exitOK || stdout != want || stderr != "" {
		t.Fatalf("stats = exit %d, stdout %q, stderr %q, want\n%s", code, stdout, stderr, want)
	}
}

// A group without a single recorded size leaves its sizes out instead of reporting 0.
func TestVaultLogsStatsLeavesOutSizesOfAGroupWithoutCounter(t *testing.T) {
	dir := statsFixture(t)
	_, stdout, _ := runWithInput(t, &Options{}, "", "vault", "logs", "stats", "--config", configIn(dir),
		"--output", "json", "--from", "2026-05-03", "--to", "2026-05-03")
	want := `{"operation":"old.tool","connection":"wiki","calls":1,"no_counter":1}`
	if !strings.Contains(stdout, want) {
		t.Errorf("stats = %s, want a group %s with no sizes", stdout, want)
	}
}

func TestVaultLogsStatsRange(t *testing.T) {
	dir := statsFixture(t)
	calls := func(args ...string) int {
		t.Helper()
		args = append([]string{"vault", "logs", "stats", "--config", configIn(dir), "--output", "json"}, args...)
		code, stdout, stderr := runWithInput(t, &Options{}, "", args...)
		var doc logsStatsDocument
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil || code != exitOK {
			t.Fatalf("stats %v = exit %d, %q, %q: %v", args, code, stdout, stderr, err)
		}
		total := 0
		for _, g := range doc.Groups {
			total += g.Calls
		}
		return total
	}
	for _, c := range []struct {
		args []string
		want int
	}{
		{nil, 6},
		{[]string{"--from", "2026-05-02"}, 5},
		{[]string{"--to", "2026-05-02"}, 4},
		{[]string{"--from", "2026-05-03", "--to", "2026-05-03"}, 2},
		{[]string{"--from", "2026-06-01"}, 0},
	} {
		if got := calls(c.args...); got != c.want {
			t.Errorf("stats %v counted %d invokes, want %d", c.args, got, c.want)
		}
	}
}

func TestVaultLogsStatsDefaultFormatAndEmptyLog(t *testing.T) {
	dir := statsFixture(t)
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "logs", "stats", "--config", configIn(dir))
	if code != exitOK || stderr != "" || !strings.Contains(stdout, "groups[3]") || !strings.Contains(stdout, "wiki.pages.get") ||
		strings.HasPrefix(stdout, "{") {
		t.Errorf("default stats = exit %d, %q, %q, want TOON", code, stdout, stderr)
	}
	empty := vaultCredentialFixture(t)
	code, stdout, _ = runWithInput(t, &Options{}, "", "vault", "logs", "stats", "--config", configIn(empty), "--output", "json")
	if code != exitOK || stdout != "{\"groups\":[]}\n" {
		t.Errorf("stats without a log = exit %d, %q", code, stdout)
	}
}

func TestVaultLogsStatsRejectsBadDays(t *testing.T) {
	dir := statsFixture(t)
	for _, args := range [][]string{
		{"--from", "05/02/2026"},
		{"--to", "tomorrow"},
		{"--from", "2026-05-03", "--to", "2026-05-02"},
	} {
		args = append([]string{"vault", "logs", "stats", "--config", configIn(dir)}, args...)
		if code, stdout, _ := runWithInput(t, &Options{}, "", args...); code == exitOK || stdout != "" {
			t.Errorf("stats %v = exit %d, stdout %q, want a usage error", args, code, stdout)
		}
	}
}

func sessionEntry(hour, session, op, result string, req, resp int64) invokelog.Entry {
	e := statsEntry("2026-05-04", hour, op, "", &resp)
	e.Session, e.Effect, e.Result, e.ReqBytes = session, "", result, &req
	return e
}

// Sessions are summed up from the same entries as the groups: baseline, navigation, and invoke, error
// responses included, while the groups keep counting only invokes.
func TestVaultLogsStatsSessions(t *testing.T) {
	dir := vaultCredentialFixture(t)
	s := "0123456789abcdef"
	invoke := sessionEntry("12", s, "wiki.pages.get", "not-found", 30, 90)
	invoke.Connection, invoke.Effect = "wiki", "read"
	writeStatsLog(t, dir, []invokelog.Entry{
		sessionEntry("10", s, invokelog.OperationMCPInitialize, "success", 100, 1000),
		sessionEntry("10", s, invokelog.OperationMCPToolsList, "success", 50, 9000),
		sessionEntry("11", s, invokelog.OperationMCPSearch, "invalid-request", 70, 200),
		invoke,
		statsEntry("2026-05-04", "13", "old.tool", "wiki", nil),
	})
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "logs", "stats", "--config", configIn(dir),
		"--output", "json")
	want := `"sessions":[{"session":"` + s + `","areas":[` +
		`{"area":"baseline","calls":2,"req_bytes":150,"resp_bytes":10000,"total_bytes":10150,"tools":[` +
		`{"operation":"mcp/tools-list","calls":1,"req_bytes":50,"resp_bytes":9000,"total_bytes":9050},` +
		`{"operation":"mcp/initialize","calls":1,"req_bytes":100,"resp_bytes":1000,"total_bytes":1100}]},` +
		`{"area":"navigation","calls":1,"req_bytes":70,"resp_bytes":200,"total_bytes":270,"tools":[` +
		`{"operation":"qatlas.search","calls":1,"req_bytes":70,"resp_bytes":200,"total_bytes":270}]},` +
		`{"area":"invoke","calls":1,"req_bytes":30,"resp_bytes":90,"total_bytes":120,"tools":[` +
		`{"operation":"wiki.pages.get","calls":1,"req_bytes":30,"resp_bytes":90,"total_bytes":120}]}]}]}` + "\n"
	if code != exitOK || stderr != "" || !strings.HasSuffix(stdout, want) {
		t.Fatalf("stats = exit %d, stdout %q, stderr %q, want a suffix\n%s", code, stdout, stderr, want)
	}
	groups := strings.SplitN(stdout, `"sessions"`, 2)[0]
	if strings.Contains(groups, "mcp/") || strings.Contains(groups, "qatlas.search") ||
		!strings.Contains(groups, `"operation":"wiki.pages.get"`) || !strings.Contains(groups, `"old.tool"`) {
		t.Errorf("groups = %s, want only the invokes", stdout)
	}

	code, stdout, stderr = runWithInput(t, &Options{}, "", "vault", "logs", "stats", "--config", configIn(dir))
	if code != exitOK || stderr != "" || !strings.Contains(stdout, "sessions") || !strings.Contains(stdout, "mcp/tools-list") ||
		strings.HasPrefix(stdout, "{") {
		t.Errorf("default stats = exit %d, %q, %q, want TOON with sessions", code, stdout, stderr)
	}
}

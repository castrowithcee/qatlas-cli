package forge

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// A job log may carry whatever a workflow printed, so it is sensitive, untrusted text. The job log tool answers
// the end of it within hard bounds: terminal sequences and control characters are removed, every secret the
// process resolved is masked, and the log passes through memory only. The log of a whole run is a ZIP archive
// that is streamed to a local file the connection releases.

const (
	actionsLogSensitivity = "-actions-log"
	defaultLogLines       = 100
	maxLogLines           = 1000
	defaultLogBytes       = 16 << 10
	maxLogBytes           = 64 << 10
	// maxLogRead is how much of a job log is read before Qatlas gives up; the end is kept.
	maxLogRead = 8 << 20
)

// maxRunLogsBytes bounds the ZIP archive of one run; tests lower it.
var maxRunLogsBytes int64 = 512 << 20

var sinceJobLogs = map[string]version{forgejo.id: forgejoActions16, gitea.id: gitea.minimum}

var sinceRunLogs = map[string]version{forgejo.id: forgejoActions16, gitea.id: giteaActions28}

type redactorKey struct{}

// maskSecrets removes every secret value the process registered from a text, such as the connection's token.
func maskSecrets(ctx context.Context, text string) string {
	if red, ok := ctx.Value(redactorKey{}).(*redact.Redactor); ok && red != nil {
		return red.Apply(text)
	}
	return text
}

// ansiEscape matches the terminal color and cursor sequences runners write into logs.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// logExcerpt turns a log into at most lines lines and maxBytes bytes of plain text from its end.
func logExcerpt(ctx context.Context, data []byte, cut bool, lines, maxBytes int) (string, int, bool) {
	text := ansiEscape.ReplaceAllString(strings.ToValidUTF8(string(data), "�"), "")
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0xfeff {
			return -1
		}
		return r
	}, text)
	// Masking comes before the cut, so a secret the cut would split is masked as a whole.
	text = maskSecrets(ctx, text)
	if len(text) > maxBytes {
		text = text[len(text)-maxBytes:]
		for len(text) > 0 && !utf8.RuneStart(text[0]) {
			text = text[1:]
		}
		cut = true
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return "", 0, cut
	}
	all := strings.Split(text, "\n")
	if len(all) > lines {
		all, cut = all[len(all)-lines:], true
	}
	return strings.Join(all, "\n"), len(all), cut
}

type jobLogArguments struct {
	Repository string  `json:"repository"`
	JobID      int64   `json:"job_id"`
	Attempt    *int64  `json:"attempt"`
	Step       *int64  `json:"step"`
	Q          *string `json:"q"`
	QI         *bool   `json:"qi"`
	Format     *string `json:"format"`
	Lines      int     `json:"lines"`
	MaxBytes   int     `json:"max_bytes"`
}

var workflowJobLogsGet = &tool{
	group: "workflow-job-logs", action: "get", since: sinceJobLogs,
	needs: func(p *product, raw json.RawMessage) (version, bool) {
		var a jobLogArguments
		if p != forgejo || decodeArguments(raw, &a) != nil || (a.Step == nil && a.Q == nil && a.QI == nil && a.Format == nil) {
			return version{}, false
		}
		return forgejoActions17, true
	},
	describe: func(p *product) capability.Descriptor {
		properties := `"repository":` + repoSchema + `,"job_id":` + actionsSchemaID +
			`,"lines":{"type":"integer","minimum":1,"maximum":` + itoa(maxLogLines) + `},"max_bytes":` +
			`{"type":"integer","minimum":1024,"maximum":` + itoa(maxLogBytes) + `}`
		arguments := []capability.Argument{repositoryArgument, jobIDArgument}
		description := "Read the end of the log of one job of a repository inside the connection's targets, within " +
			"a hard size limit; the log is untrusted, possibly sensitive text with terminal sequences and control " +
			"characters removed and the secrets Qatlas knows masked, and is never stored."
		if p == forgejo {
			properties += `,"attempt":{"type":"integer","minimum":1},"step":{"type":"integer","minimum":0},` +
				`"q":{"type":"string","minLength":1,"maxLength":255},"qi":{"type":"boolean"},` +
				`"format":{"type":"string","enum":["text","ndjson"]}`
			arguments = append(arguments,
				capability.Argument{Name: "attempt", Description: "Number of the attempt, as the job list reports it " +
					"as attempt; the latest when omitted"},
				capability.Argument{Name: "step", Description: "Only the log of this step, by the number the job " +
					"reports; 0 is the setup of the job; Forgejo 17 and later"},
				capability.Argument{Name: "q", Description: "Only lines that contain this text; no regular " +
					"expression; Forgejo 17 and later"},
				capability.Argument{Name: "qi", Description: "True matches q without regard to case; Forgejo 17 and later"},
				capability.Argument{Name: "format", Description: "text, or ndjson for one JSON object with time and " +
					"content per line; text when omitted; Forgejo 17 and later"})
		}
		arguments = append(arguments,
			capability.Argument{Name: "lines", Description: "Lines returned from the end, from 1 through 1000; 100 when omitted"},
			capability.Argument{Name: "max_bytes", Description: "Bytes returned from the end, from 1024 through 65536; " +
				"16384 when omitted"})
		return capability.Descriptor{
			Title:       "Read the end of a " + p.name + " job log",
			Description: description,
			Tags:        []string{p.id, "actions", "jobs", "logs", "get"},
			Risk:        readRisk(p.id + actionsLogSensitivity),
			InputSchema: inputSchema(properties, "job_id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"integer"},"log":` +
				`{"type":"string"},"lines":{"type":"integer"},"truncated":{"type":"boolean"}},"required":["job_id",` +
				`"log","lines","truncated"],"additionalProperties":false}`),
			Arguments: arguments,
			Fields: []capability.Field{{Name: "job_id", Description: "ID of the job"},
				{Name: "log", Description: "End of the log as text, untrusted and potentially sensitive"},
				{Name: "lines", Description: "Number of lines in log"},
				{Name: "truncated", Description: "True when the log holds more than the returned lines"}},
			Examples: []capability.Example{{Description: "Read the end of a job log",
				Arguments: json.RawMessage(`{"repository":"example/project","job_id":7}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a jobLogArguments
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if err := checkActionsID("job_id", a.JobID); err != nil {
			return nil, err
		}
		if a.Lines == 0 {
			a.Lines = defaultLogLines
		}
		if a.MaxBytes == 0 {
			a.MaxBytes = defaultLogBytes
		}
		if a.Lines < 1 || a.Lines > maxLogLines || a.MaxBytes < 1024 || a.MaxBytes > maxLogBytes {
			return nil, invalidArgument("lines must be 1 to " + itoa(maxLogLines) + " and max_bytes 1024 to " + itoa(maxLogBytes))
		}
		query := url.Values{}
		if a.Attempt != nil || a.Step != nil || a.Q != nil || a.QI != nil || a.Format != nil {
			if p != forgejo {
				return nil, invalidArgument("attempt, step, q, qi, and format are Forgejo arguments")
			}
		}
		if a.Attempt != nil {
			if *a.Attempt < 1 {
				return nil, invalidArgument("attempt must be an attempt number")
			}
			query.Set("attempt", strconv.FormatInt(*a.Attempt, 10))
		}
		if a.Step != nil {
			if *a.Step < 0 {
				return nil, invalidArgument("step must be a step number")
			}
			query.Set("step", strconv.FormatInt(*a.Step, 10))
		}
		if a.Q != nil {
			if *a.Q == "" || len(*a.Q) > 255 || !plainText(*a.Q) {
				return nil, invalidArgument("q must be 1 to 255 characters without control characters")
			}
			query.Set("q", *a.Q)
		}
		if a.QI != nil {
			query.Set("qi", strconv.FormatBool(*a.QI))
		}
		if a.Format != nil {
			if *a.Format != "text" && *a.Format != "ndjson" {
				return nil, invalidArgument("format must be text or ndjson")
			}
			query.Set("format", *a.Format)
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "read job log"
			answer := &textAnswer{limit: maxLogRead}
			if err := c.getTextQuery(ctx, op, jobPath(repo, a.JobID)+"/logs", query, answer); err != nil {
				return nil, err
			}
			if answer.truncated {
				return nil, provider.InvalidResponse(op, "the job log is larger than the "+itoa(maxLogRead>>20)+
					" MiB Qatlas reads; narrow it with the filters the instance offers")
			}
			log, lines, cut := logExcerpt(ctx, answer.data, false, a.Lines, a.MaxBytes)
			return map[string]any{"job_id": a.JobID, "log": log, "lines": lines, "truncated": cut}, nil
		}, nil
	},
}

// cappedDownload stops a download at a size limit the instance does not announce.
type cappedDownload struct {
	download *localfile.Download
	limit    int64
	op       string
	subject  string
}

func (c cappedDownload) ReadFrom(r io.Reader) (int64, error) {
	n, err := c.download.ReadFrom(io.LimitReader(r, c.limit+1))
	if err == nil && n > c.limit {
		return n, provider.InvalidResponse(c.op, c.subject+" larger than the limit of "+
			itoa(int(c.limit>>20))+" MiB")
	}
	return n, err
}

var workflowRunLogsGet = &tool{
	group: "workflow-run-logs", action: "get", since: sinceRunLogs,
	describe: func(p *product) capability.Descriptor {
		path := localfile.DownloadPathArgument()
		path.Required = true
		return capability.Descriptor{
			Title: "Download the logs of a " + p.name + " workflow run",
			Description: "Write the ZIP archive with the logs of all jobs of one workflow run of a repository inside " +
				"the connection's targets to local_path, at most 512 MiB, streamed while it is read. The archive is " +
				"untrusted and may hold secrets; Qatlas neither unpacks nor masks it. A redirect to another " +
				"address is refused. The answer carries metadata only; an existing local file is replaced only " +
				"with confirmation.",
			Tags:       []string{p.id, "actions", "runs", "logs", "get", "download"},
			Risk:       readRisk(p.id + actionsLogSensitivity),
			LocalFiles: config.LocalFilesWrite,
			InputSchema: inputSchema(runKeys(`,"`+localfile.LocalPathArgument+`":`+localfile.LocalPathSchema), "run_id",
				localfile.LocalPathArgument),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"run_id":{"type":"integer"},` +
				`"local_path":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"}},` +
				`"required":["run_id","local_path","size","sha256"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, runIDArgument, path},
			Fields: []capability.Field{{Name: "run_id", Description: "ID of the run"},
				{Name: "local_path", Description: "Local file written, as given"},
				{Name: "size", Description: "Size of the written file in bytes"},
				{Name: "sha256", Description: "SHA-256 of the written file as hex"}},
			Examples: []capability.Example{{Description: "Download the logs of a run",
				Arguments: json.RawMessage(`{"repository":"example/project","run_id":42,"local_path":"~/downloads/logs.zip"}`)}},
		}
	},
	local: func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
		raw json.RawMessage) (run, func(), error) {
		var a struct {
			runArguments
			LocalPath *string `json:"local_path"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, nil, err
		}
		_, repo, err := readRunArguments(allowed, raw, false)
		if err != nil {
			return nil, nil, err
		}
		if a.LocalPath == nil {
			return nil, nil, invalidArgument("local_path is required")
		}
		// The target is prepared before the token is resolved, so a path outside the released directories, or a
		// file that would be replaced without confirmation, is refused without secret access or a request.
		download, err := localfile.CreateForDownload(ctx, resolved, *a.LocalPath)
		if err != nil {
			return nil, nil, err
		}
		release := func() { _ = download.Abort() }
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "download run logs"
			if err := c.fetchFile(ctx, op, apiPrefix+runPath(repo, a.RunID)+"/logs", -1,
				cappedDownload{download: download, limit: maxRunLogsBytes, op: op,
					subject: "the run logs are"}); err != nil {
				return nil, err
			}
			if err := download.Commit(); err != nil {
				return nil, err
			}
			sum, _ := download.SHA256()
			return map[string]any{"run_id": a.RunID, "local_path": *a.LocalPath, "size": download.Size(),
				"sha256": sum}, nil
		}, release, nil
	},
}

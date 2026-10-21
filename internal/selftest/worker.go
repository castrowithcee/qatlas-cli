package selftest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// WorkerProtocol is the version of the line protocol between the runner and a writer process. Both sides
// are normally the same binary; the check catches a mismatch if they are not.
//
// The runner starts a worker with no parameters beyond WorkerCommand, then:
//
//	runner -> worker  {"protocol":1,"path":"...","name":"..."}\n
//	worker -> runner  ready\n
//	runner -> worker  go\n
//	worker -> runner  {"waited_ns":N,"total_ns":N}\n
//
// A worker that fails writes one line to stderr and exits non-zero.
const WorkerProtocol = 1

type workerInit struct {
	Protocol int    `json:"protocol"`
	Path     string `json:"path"`
	Name     string `json:"name"`
}

type workerResult struct {
	WaitedNs int64 `json:"waited_ns"`
	TotalNs  int64 `json:"total_ns"`
}

// WorkerCommand says how to start a writer process: the program, its arguments, and extra environment
// entries appended to the inherited environment. The started process must call RunWorker.
type WorkerCommand struct {
	Program string
	Args    []string
	Env     []string
}

// RunWorker is the body of a writer process. It reads the protocol from in, waits for the start signal so
// all writers ask for the lock together, runs one config.Store.Update on the configuration the runner
// names, and reports how long the lock request blocked and how long the whole Update took. It returns the
// process exit code; errors go to errOut and name no secret.
func RunWorker(in io.Reader, out, errOut io.Writer) int {
	fail := func(code int, format string, args ...any) int {
		fmt.Fprintf(errOut, "selftest worker: "+format+"\n", args...)
		return code
	}
	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil {
		return fail(1, "cannot read the start message: %v", err)
	}
	var init workerInit
	if err := json.Unmarshal([]byte(line), &init); err != nil {
		return fail(2, "the start message is not valid: %v", err)
	}
	if init.Protocol != WorkerProtocol {
		return fail(2, "protocol version %d is not supported, this program speaks version %d; "+
			"run the selftest with the same program version", init.Protocol, WorkerProtocol)
	}
	if init.Path == "" || init.Name == "" {
		return fail(2, "the start message lacks a path or a name")
	}

	var waited time.Duration
	store := config.NewStore(init.Path, syntheticCatalog{}).Observed(config.Observer{
		LockWaited: func(d time.Duration) { waited = d },
	})
	fmt.Fprintln(out, "ready")
	if _, err := r.ReadString('\n'); err != nil {
		return fail(1, "cannot read the start signal: %v", err)
	}
	start := time.Now()
	err = store.Update(func(cfg *config.Config) error {
		return cfg.SetService(init.Name, config.Service{Provider: syntheticProvider,
			BaseURL: "https://" + init.Name + ".example.invalid"})
	})
	if err != nil {
		return fail(1, "update failed: %v", err)
	}
	res, _ := json.Marshal(workerResult{WaitedNs: waited.Nanoseconds(), TotalNs: time.Since(start).Nanoseconds()})
	fmt.Fprintf(out, "%s\n", res)
	return 0
}

// LockWaitRound is the outcome of one round of writer processes.
type LockWaitRound struct {
	// Waits is how long each writer's lock request blocked.
	Waits []time.Duration
	// Total is the span from the release of the writers until the last one reported.
	Total time.Duration
}

// LockWaitValues summarizes rounds as measurement values: wait percentiles over all writers of all rounds and
// the mean span per round.
func LockWaitValues(rounds []LockWaitRound) []Value {
	var waits []time.Duration
	var total time.Duration
	for _, r := range rounds {
		waits = append(waits, r.Waits...)
		total += r.Total
	}
	st := StatsOf(waits)
	var mean float64
	if len(rounds) > 0 {
		mean = ms(total) / float64(len(rounds))
	}
	return []Value{{"wait-p50-ms", ms(st.P50)}, {"wait-p95-ms", ms(st.P95)}, {"wait-max-ms", ms(st.Max)},
		{"total-ms", mean}}
}

// maxStderr bounds the worker output kept for an error message.
const maxStderr = 2048

type limitedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := maxStderr - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(b.buf.String())
}

type workerProc struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	stderr limitedBuffer
	waited bool
}

// RunLockWaitRound starts writers processes with cmd on the configuration at path, releases them together,
// and returns each one's lock wait. If span is set, it is called with true when the writers are released and
// with false when the last one has reported, so a caller can time just that span. When it returns, every
// process has exited or been killed and waited for, also when ctx ends.
func RunLockWaitRound(ctx context.Context, cmd WorkerCommand, path string, writers int,
	span func(running bool)) (LockWaitRound, error) {
	var round LockWaitRound
	if cmd.Program == "" {
		return round, errors.New("no worker program is configured for the lock-wait measurement")
	}
	procs := make([]*workerProc, 0, writers)
	// stop ends and waits for every process that is still running.
	stop := func() {
		for _, p := range procs {
			if p.waited {
				continue
			}
			_ = p.in.Close()
			if p.cmd.Process != nil {
				_ = p.cmd.Process.Kill()
			}
			_ = p.cmd.Wait()
			p.waited = true
		}
	}
	defer stop()
	// fail kills everything first, so the process output is complete when the message is built.
	fail := func(i int, what string, err error) (LockWaitRound, error) {
		stop()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return LockWaitRound{}, ctxErr
		}
		msg := fmt.Sprintf("writer process %d: %s", i+1, what)
		if err != nil {
			msg += ": " + err.Error()
		}
		if out := procs[i].stderr.String(); out != "" {
			msg += ": " + out
		}
		return LockWaitRound{}, errors.New(msg)
	}

	for i := 0; i < writers; i++ {
		c := exec.CommandContext(ctx, cmd.Program, cmd.Args...)
		c.Env = append(os.Environ(), cmd.Env...)
		c.WaitDelay = 5 * time.Second
		p := &workerProc{cmd: c}
		c.Stderr = &p.stderr
		in, err := c.StdinPipe()
		if err != nil {
			return round, err
		}
		out, err := c.StdoutPipe()
		if err != nil {
			return round, err
		}
		p.in, p.out = in, bufio.NewReader(out)
		if err := c.Start(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return round, ctxErr
			}
			return round, fmt.Errorf("cannot start the writer process: %w", err)
		}
		procs = append(procs, p)
		hello, _ := json.Marshal(workerInit{Protocol: WorkerProtocol, Path: path, Name: fmt.Sprintf("svc%d", i)})
		if _, err := fmt.Fprintf(in, "%s\n", hello); err != nil {
			return fail(i, "cannot send the start message", err)
		}
	}
	for i, p := range procs {
		line, err := p.out.ReadString('\n')
		if err != nil || strings.TrimSpace(line) != "ready" {
			return fail(i, "did not become ready", err)
		}
	}

	if span != nil {
		span(true)
	}
	start := time.Now()
	for i, p := range procs {
		if _, err := io.WriteString(p.in, "go\n"); err != nil {
			if span != nil {
				span(false)
			}
			return fail(i, "cannot send the start signal", err)
		}
	}
	round.Waits = make([]time.Duration, 0, writers)
	for i, p := range procs {
		line, err := p.out.ReadString('\n')
		var res workerResult
		if err == nil {
			err = json.Unmarshal([]byte(line), &res)
		}
		if err != nil {
			if span != nil {
				span(false)
			}
			return fail(i, "did not report a result", err)
		}
		round.Waits = append(round.Waits, time.Duration(res.WaitedNs))
	}
	round.Total = time.Since(start)
	if span != nil {
		span(false)
	}
	for i, p := range procs {
		err := p.cmd.Wait()
		p.waited = true
		if err != nil {
			return fail(i, "did not exit cleanly", err)
		}
	}
	return round, nil
}

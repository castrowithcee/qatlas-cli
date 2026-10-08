//go:build windows

package vaultproc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// StartProcess starts 'qatlas vault serve' for the vault at configPath, detached from this process and its
// console, hands it snap through an inherited pipe, and waits until client reaches it. It is the shared core
// 'qatlas vault unlock' and the TUI's own 'ctrl+l' unlock both run, so a vault process is started exactly
// the same way whichever asks; only how the passphrase that produced snap was asked differs, and neither
// belongs to this package.
//
// The program started is this same running binary (see os.Executable), so the process passes the check a
// vault process makes of every client; its 'vault serve' subcommand is what actually answers the handover
// and the report, and lives in package cli, the only package that wires up commands. On Windows the
// handover and the report are its standard input and output, two anonymous pipes, and its standard error is
// NUL; it inherits no other handle. It starts without a console (DETACHED_PROCESS), in a process group of its
// own (CREATE_NEW_PROCESS_GROUP), in the system directory, so it holds no console and no directory of the
// session that started it, and closing the terminal, Windows Terminal and conhost alike, does not end it. It
// also leaves the job object this process runs in, where the job lets it (CREATE_BREAKAWAY_FROM_JOB); where
// it does not, it is started inside the job and ends with it. Neither the key nor any secret ever appears in
// its arguments or its environment: they travel through the pipe alone, which the process closes once it has
// read them.
//
// No Windows vault process is ever started as a successor: an update does not hand the vault over here.
func StartProcess(ctx context.Context, configPath string, snap vault.Snapshot,
	client *Client) (Status, error) {
	program, err := os.Executable()
	if err != nil {
		return Status{}, fmt.Errorf("cannot find this program to start the vault process: %w", err)
	}
	deadline := startDeadline(ctx)
	if err := startServe(program, serveArgs(configPath, false), snap, deadline); err != nil {
		return Status{}, err
	}
	return awaitProcess(ctx, client, deadline)
}

// startServe starts program with args as a vault process detached from this one, hands it snap on its
// standard input, and waits until deadline for its report on its standard output. It returns an error
// unless the report was ReportReady or ReportRunning. A process that reported nothing by deadline is ended,
// which ends both pipes with it; any other process is left running, and this process lets go of it.
func startServe(program string, args []string, snap vault.Snapshot, deadline time.Time) error {
	handoverRead, handoverWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer handoverWrite.Close()
	reportRead, reportWrite, err := os.Pipe()
	if err != nil {
		_ = handoverRead.Close()
		return err
	}
	defer reportRead.Close()

	dir, err := windows.GetSystemDirectory()
	if err != nil {
		dir = filepath.VolumeName(program) + `\`
	}
	start := func(flags uint32) (*exec.Cmd, error) {
		cmd := exec.Command(program, args...)
		cmd.Dir = dir
		cmd.Stdin = handoverRead // the handover
		cmd.Stdout = reportWrite // the report
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		return cmd, cmd.Start()
	}
	flags := uint32(windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP)
	cmd, err := start(flags | windows.CREATE_BREAKAWAY_FROM_JOB)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// The job this process runs in lets no process leave it.
		cmd, err = start(flags)
	}
	// The process holds its own ends now; closing them here makes its end, or its report, an end of file.
	_ = handoverRead.Close()
	_ = reportWrite.Close()
	if err != nil {
		return fmt.Errorf("cannot start the vault process: %w", err)
	}

	// An anonymous pipe takes no deadline here. A process that has neither taken the vault nor reported by
	// then is ended instead, which ends both pipes with it. Once the start is decided, this process lets go
	// of its handle to the vault process: a handle opened before the vault process hardened itself keeps
	// every right, and nothing of this process should hold one for longer than the start.
	var mu sync.Mutex
	timedOut, released := false, false
	watchdog := time.AfterFunc(time.Until(deadline), func() {
		mu.Lock()
		defer mu.Unlock()
		if !released {
			timedOut = true
			_ = cmd.Process.Kill()
		}
	})
	defer func() {
		watchdog.Stop()
		mu.Lock()
		released = true
		mu.Unlock()
		_ = cmd.Process.Release()
	}()

	data, err := json.Marshal(snap)
	if err != nil {
		return errors.New("cannot encode the vault for the vault process")
	}
	_, err = handoverWrite.Write(data)
	clear(data)
	_ = handoverWrite.Close()
	if err != nil {
		return errors.New("the vault process did not take the vault")
	}

	report, _ := bufio.NewReader(io.LimitReader(reportRead, 4096)).ReadString('\n')
	report = strings.TrimSpace(report)
	mu.Lock()
	ended := timedOut
	mu.Unlock()
	switch {
	case report == ReportReady, report == ReportRunning:
		return nil
	case report != "":
		return fmt.Errorf("the vault process did not start: %s", report)
	case ended:
		return errors.New("the vault process did not report within " + StartTimeout.String())
	default:
		return errors.New("the vault process ended before it was ready")
	}
}

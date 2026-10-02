//go:build windows

package vaultmigrate

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
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// StartProcess starts 'qatlas vault serve' for the vault at configPath, detached from this process and its
// session, hands it snap through an inherited pipe, and waits until client reaches it. It is the shared core
// 'qatlas vault unlock' and the TUI's own 'ctrl+l' unlock both run, so a vault process is started exactly
// the same way whichever asks; only how the passphrase that produced snap was asked differs, and neither
// belongs to this package.
//
// The program started is this same running binary (see os.Executable), so the process passes the check a
// vault process makes of every client; its 'vault serve' subcommand is what actually answers the handover
// and the report, and lives in package cli, the only package that wires up commands. On Windows the
// handover and the report are its standard input and output, two anonymous pipes, and its standard error
// is NUL. It starts without a console (DETACHED_PROCESS), in a process group of its own
// (CREATE_NEW_PROCESS_GROUP), in the system directory, so it holds no console and no directory of the
// session that started it. It also leaves the job object this process runs in (CREATE_BREAKAWAY_FROM_JOB),
// such as the one an SSH server ends with its session; where the job does not let it leave, it is started
// inside the job instead, and ends with it. Neither the key nor any secret ever appears in its arguments or
// its environment: they travel through the pipe alone, which the process closes once it has read them.
func StartProcess(ctx context.Context, configPath string, snap vault.Snapshot,
	client *vaultproc.Client) (vaultproc.Status, error) {
	program, err := os.Executable()
	if err != nil {
		return vaultproc.Status{}, fmt.Errorf("cannot find this program to start the vault process: %w", err)
	}
	handoverRead, handoverWrite, err := os.Pipe()
	if err != nil {
		return vaultproc.Status{}, err
	}
	defer handoverWrite.Close()
	reportRead, reportWrite, err := os.Pipe()
	if err != nil {
		_ = handoverRead.Close()
		return vaultproc.Status{}, err
	}
	defer reportRead.Close()

	dir, err := windows.GetSystemDirectory()
	if err != nil {
		dir = filepath.VolumeName(program) + `\`
	}
	start := func(flags uint32) (*exec.Cmd, error) {
		cmd := exec.Command(program, "vault", "serve", "--config", configPath)
		cmd.Dir = dir
		cmd.Stdin = handoverRead
		cmd.Stdout = reportWrite
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
		return vaultproc.Status{}, fmt.Errorf("cannot start the vault process: %w", err)
	}
	// Releases the process should it end while this one still runs; it is not waited for otherwise.
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(StartTimeout)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	// An anonymous pipe takes no deadline here. A process that has neither taken the vault nor reported by
	// then is ended instead, which ends both pipes with it.
	var ended atomic.Bool
	watchdog := time.AfterFunc(time.Until(deadline), func() {
		ended.Store(true)
		_ = cmd.Process.Kill()
	})
	defer watchdog.Stop()

	data, err := json.Marshal(snap)
	if err != nil {
		return vaultproc.Status{}, errors.New("cannot encode the vault for the vault process")
	}
	_, err = handoverWrite.Write(data)
	clear(data)
	_ = handoverWrite.Close()
	if err != nil {
		return vaultproc.Status{}, errors.New("the vault process did not take the vault")
	}

	report, _ := bufio.NewReader(io.LimitReader(reportRead, 4096)).ReadString('\n')
	watchdog.Stop()
	timedOut := ended.Load()
	report = strings.TrimSpace(report)
	switch {
	case report == ReportReady, report == ReportRunning:
	case report != "":
		return vaultproc.Status{}, fmt.Errorf("the vault process did not start: %s", report)
	case timedOut:
		return vaultproc.Status{}, errors.New("the vault process did not report within " + StartTimeout.String())
	default:
		return vaultproc.Status{}, errors.New("the vault process ended before it was ready")
	}

	// Whichever process listens, the one just started or one that won a race with it, it has to answer the
	// check every client makes before it counts as started.
	for {
		status, err := client.Status(ctx)
		if err == nil {
			return status, nil
		}
		if !errors.Is(err, vaultproc.ErrNotRunning) || time.Now().After(deadline) {
			return vaultproc.Status{}, fmt.Errorf("the vault process does not answer: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

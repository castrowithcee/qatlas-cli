//go:build linux || darwin

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
	"strings"
	"syscall"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// HandoverFD and ReportFD are the descriptors a vault process inherits from whatever started it with
// StartProcess: the handover it reads the vault's key and secrets from, and the report it answers on once
// it listens or failed to. Both 'qatlas vault unlock' and the TUI's own 'ctrl+l' start a vault process this
// same way, so the numbers, and the words on ReportFD (see ReportReady), are exported here instead of kept
// as a matching pair of unexported constants in two packages that would have to be edited together.
//
// They are also the successor contract: a vault process of one release starts the vault process of the next
// release, its successor, with StartSuccessor, which an update installed. What one release starts the other
// with therefore stays as it is, or changes only so that a release still understands what its predecessor
// sends: the arguments 'vault serve --config <path>' followed by SuccessorFlag (see serveArgs), these two
// descriptors, the JSON of vault.Snapshot on HandoverFD, locks_at included, and ReportReady on ReportFD once
// the successor listens at vaultproc.NextPath of the vault socket, anything else being the reason it did not
// start.
const (
	HandoverFD = 3
	ReportFD   = 4
)

// StartProcess starts 'qatlas vault serve' for the vault at configPath, detached from this process and its
// session, hands it snap through an inherited pipe, and waits until client reaches it. It is the shared core
// 'qatlas vault unlock' and the TUI's own 'ctrl+l' unlock both run, so a vault process is started exactly
// the same way whichever asks; only how the passphrase that produced snap was asked differs, and neither
// belongs to this package.
//
// The program started is this same running binary (see os.Executable), so the process passes the check a
// vault process makes of every client; its 'vault serve' subcommand is what actually answers the handover
// and the report, and lives in package cli, the only package that wires up commands. Its standard streams
// are /dev/null and its working directory is /, so it holds no terminal and no directory of the session
// that started it. Neither the key nor any secret ever appears in its arguments or its environment: they
// travel through the pipe alone, which the process closes once it has read them.
func StartProcess(ctx context.Context, configPath string, snap vault.Snapshot,
	client *vaultproc.Client) (vaultproc.Status, error) {
	program, err := os.Executable()
	if err != nil {
		return vaultproc.Status{}, fmt.Errorf("cannot find this program to start the vault process: %w", err)
	}
	deadline := startDeadline(ctx)
	if _, _, _, err := startServe(program, serveArgs(configPath, false), snap, deadline); err != nil {
		return vaultproc.Status{}, err
	}
	return awaitProcess(ctx, client, deadline)
}

// StartSuccessor starts 'qatlas vault serve' as the successor of the running vault process, this one, from
// program, the file an update installed at the path this process was started from and that the process
// checked against the release, for the vault at configPath, and hands it snap as StartProcess does. It returns
// once the successor reported that it listens at vaultproc.NextPath of the vault socket, with its process id
// and a function that ends it; moving its socket onto the vault socket is the caller's. Any failure ends the
// successor before it returns. It is a vaultproc.StartSuccessor.
//
// The successor is started by the path itself, so the path the kernel records for it is the path clients
// run qatlas from. What it is started with is the successor contract; see HandoverFD.
func StartSuccessor(ctx context.Context, program, configPath string, snap vault.Snapshot) (int, func(), error) {
	process, done, report, err := startServe(program, serveArgs(configPath, true), snap, startDeadline(ctx))
	if err == nil && report != ReportReady {
		// Only a process this one started listens beside the vault socket, never one that found another
		// already running.
		err = fmt.Errorf("the successor did not start: %s", report)
	}
	if err != nil {
		if process != nil {
			endProcess(process, done)
		}
		return 0, nil, err
	}
	return process.Pid, func() { endProcess(process, done) }, nil
}

// endProcess kills process and waits briefly until it is gone. A killed vault process takes its secrets
// with it; it never wrote them anywhere.
func endProcess(process *os.Process, done <-chan struct{}) {
	_ = process.Kill()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// startServe starts program with args as a vault process detached from this one and its session, hands it
// snap through HandoverFD, and waits until deadline for its report on ReportFD. It returns the process, if it
// was started, with a channel closed once it ended, and the report; an error unless the report was
// ReportReady or ReportRunning. The process is left running in every case: the caller decides whether it ends.
func startServe(program string, args []string, snap vault.Snapshot, deadline time.Time) (*os.Process,
	<-chan struct{}, string, error) {
	handoverRead, handoverWrite, err := os.Pipe()
	if err != nil {
		return nil, nil, "", err
	}
	defer handoverWrite.Close()
	reportRead, reportWrite, err := os.Pipe()
	if err != nil {
		_ = handoverRead.Close()
		return nil, nil, "", err
	}
	defer reportRead.Close()

	cmd := exec.Command(program, args...)
	cmd.Dir = "/"
	cmd.ExtraFiles = []*os.File{handoverRead, reportWrite} // HandoverFD and ReportFD
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	// The process holds its own ends now; closing them here makes its end, or its report, an end of file.
	_ = handoverRead.Close()
	_ = reportWrite.Close()
	if err != nil {
		return nil, nil, "", fmt.Errorf("cannot start the vault process: %w", err)
	}
	// Reaps the process should it end while this one still runs; it is not waited for otherwise.
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	data, err := json.Marshal(snap)
	if err != nil {
		return cmd.Process, done, "", errors.New("cannot encode the vault for the vault process")
	}
	_ = handoverWrite.SetWriteDeadline(deadline)
	_, err = handoverWrite.Write(data)
	clear(data)
	_ = handoverWrite.Close()
	if err != nil {
		return cmd.Process, done, "", errors.New("the vault process did not take the vault")
	}

	_ = reportRead.SetReadDeadline(deadline)
	report, err := bufio.NewReader(io.LimitReader(reportRead, 4096)).ReadString('\n')
	report = strings.TrimSpace(report)
	switch {
	case report == ReportReady, report == ReportRunning:
		return cmd.Process, done, report, nil
	case report != "":
		return cmd.Process, done, report, fmt.Errorf("the vault process did not start: %s", report)
	case errors.Is(err, os.ErrDeadlineExceeded):
		return cmd.Process, done, "", errors.New("the vault process did not report within " + StartTimeout.String())
	default:
		return cmd.Process, done, "", errors.New("the vault process ended before it was ready")
	}
}

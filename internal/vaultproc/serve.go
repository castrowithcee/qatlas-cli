package vaultproc

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// MaxHandover bounds what a vault process reads from its handover, a vault's key and its secrets.
const MaxHandover = 16 << 20

// StartTimeout bounds how long StartProcess waits for the vault process it started.
const StartTimeout = 10 * time.Second

// ReportReady and ReportRunning are the words a vault process reports its start with on its report pipe.
// Anything else is the reason it did not start, which never carries a secret.
const (
	ReportReady   = "ready"
	ReportRunning = "running"
)

// serveArgs are the arguments of 'qatlas vault serve' for the configuration at configPath, as a successor
// when successor is set. On Linux and macOS they are part of the successor contract; see HandoverFD there.
func serveArgs(configPath string, successor bool) []string {
	args := []string{"vault", "serve", "--config", configPath}
	if successor {
		args = append(args, SuccessorFlag)
	}
	return args
}

// startDeadline is StartTimeout from now, or the end of ctx if that comes first.
func startDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(StartTimeout)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	return deadline
}

// awaitProcess waits until client reaches a vault process, at the latest until deadline, once StartProcess
// started one. Whichever process listens, the one just started or one that won a race with it, it has to
// answer the check every client makes before it counts as started.
func awaitProcess(ctx context.Context, client *Client, deadline time.Time) (Status, error) {
	for {
		status, err := client.Status(ctx)
		if err == nil {
			return status, nil
		}
		if !errors.Is(err, ErrNotRunning) || time.Now().After(deadline) {
			return Status{}, fmt.Errorf("the vault process does not answer: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

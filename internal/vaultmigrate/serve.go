package vaultmigrate

import "time"

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

//go:build linux

package vaultproc

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

// TestLogOverTheSocket runs log and logcheck through a real socket and the real peer checks: the entries
// the client hands over come back signed, and a batch of lines larger than one message is checked in
// several requests, which a single request of that size could not be.
func TestLogOverTheSocket(t *testing.T) {
	dir := t.TempDir()
	path := socketIn(t)
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	s := NewServer(testKey, testSecrets(), testBindings())
	s.KeepLog(dir, 90)
	done := serve(t, s, l)
	c := NewClient(path, testRecipient)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := c.Log(ctx, goodEntry().fields()); err != nil {
			t.Fatalf("Log() error = %v", err)
		}
	}
	report, err := invokelog.VerifyWith(dir, c.LogChecker(ctx))
	if err != nil || report.Broken || !report.Checked || report.Days[0].Unverified != 0 || report.Days[0].Entries != 2 {
		t.Fatalf("VerifyWith(process) = %+v, %v, want 2 checked, signed entries", report, err)
	}

	line := logLines(t, dir)[0]
	var lines [][]byte
	for total := 0; total <= 2*MaxMessage; total += len(line) {
		lines = append(lines, line)
	}
	overlong := bytes.Repeat([]byte("x"), maxLogCheckBatch+1)
	changed := bytes.Replace(line, []byte(`"success"`), []byte(`"timeout"`), 1)
	lines = append(lines, overlong, changed)
	valid, err := c.CheckLog(ctx, lines)
	if err != nil || len(valid) != len(lines) {
		t.Fatalf("CheckLog() = %d results, %v, want %d", len(valid), err, len(lines))
	}
	for i, ok := range valid[:len(valid)-2] {
		if !ok {
			t.Fatalf("line %d reported changed", i)
		}
	}
	if valid[len(valid)-2] || valid[len(valid)-1] {
		t.Fatal("an overlong or changed line reported valid")
	}

	if err := c.Lock(ctx); err != nil {
		t.Fatal(err)
	}
	waitServed(t, done)
	if err := c.Log(ctx, goodEntry().fields()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Log() after Lock() error = %v, want ErrNotRunning", err)
	}
}

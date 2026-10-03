package approval

import (
	"fmt"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

// The sources an Origin names.
const (
	// OriginTUI, OriginWeb, and OriginCLI: qatlas itself changed the connection on that surface, as its
	// invocation log entry says.
	OriginTUI = "tui"
	OriginWeb = "web"
	OriginCLI = "cli"
	// OriginOutside: no log entry explains the change, so the configuration file was changed by something
	// that is not qatlas.
	OriginOutside = "outside"
	// OriginVault: only the credential entry was stored anew in the vault; the configuration did not change.
	OriginVault = "vault"
	// OriginUnknown: the log could not be read.
	OriginUnknown = "unknown"
)

// Origin says where the change that left a connection open came from. It is information for the person who
// decides, never an authorization: it changes neither what an approval covers nor what is approved.
type Origin struct {
	// Source is one of the Origin constants.
	Source string
	// Time is when qatlas logged the change, or, for OriginOutside, when the configuration file was last
	// modified; zero when that is not known.
	Time time.Time
	// Seq is the sequence number of the log entry found, 0 when none was.
	Seq uint64
	// Verified is true for a log entry whose check value matched and whose place in the hash chain is
	// intact; it only means something when Seq is not 0.
	Verified bool
	// Credential names the vault credential of OriginVault.
	Credential string
}

// Text is the origin as one line a person reads, times in the local zone. It holds no value of the
// connection.
func (o Origin) Text() string {
	when := func(prefix string) string {
		if o.Time.IsZero() {
			return ""
		}
		return prefix + o.Time.In(time.Local).Format("2006-01-02 15:04")
	}
	switch o.Source {
	case OriginTUI, OriginWeb, OriginCLI:
		text := "changed in qatlas " + o.Source + when(", ")
		if !o.Verified {
			text += " (unverified)"
		}
		return text
	case OriginOutside:
		modified := " (last modified time unknown)"
		if !o.Time.IsZero() {
			modified = " (last modified " + o.Time.In(time.Local).Format("2006-01-02 15:04") + ")"
		}
		return "changed outside qatlas: config.yaml edited directly" + modified
	case OriginVault:
		return fmt.Sprintf("the vault entry of %s was stored anew; the configuration did not change", o.Credential)
	}
	return "origin unknown: the log could not be read"
}

// storedAnewOnly reports whether the only difference of change is a credential entry stored anew.
func storedAnewOnly(change Change) bool {
	return !change.New && len(change.Fields) == 1 && change.Fields[0].Field == FieldCredential &&
		strings.HasSuffix(change.Fields[0].After, " (stored anew)")
}

// since is the start of the log window that can explain change: its last approval, or for a connection
// never approved the retention window ending at now.
func since(change Change, now time.Time, retentionDays int) time.Time {
	if !change.Approved.IsZero() {
		return change.Approved
	}
	return now.AddDate(0, 0, -max(retentionDays, 1))
}

// LogWindow is the first day, in the local zone, of the log entries that can explain change; the logs
// section opens on it.
func LogWindow(change Change, now time.Time, retentionDays int) time.Time {
	return since(change, now, retentionDays).In(time.Local)
}

// Origins finds the origin of every change that has one to find: for each, the youngest log entry
// config.connection.create or .change of that connection written after its last approval, read from the day
// files of vaultDir since the oldest such approval (a connection never approved is looked up over the last
// retentionDays days) and checked with checker, which may be nil. A change whose only difference is a
// credential stored anew is OriginVault and needs no log. Where no entry is found, the change was made
// outside qatlas, to the configuration file last modified at configModTime. A log that cannot be read leaves
// OriginUnknown, never an error.
func Origins(vaultDir string, checker invokelog.Checker, changes []Change, configModTime, now time.Time,
	retentionDays int) map[string]Origin {
	out := make(map[string]Origin, len(changes))
	var lookup []Change
	var oldest time.Time
	for _, change := range changes {
		if storedAnewOnly(change) {
			out[change.Connection] = Origin{Source: OriginVault, Credential: change.After.Credential}
			continue
		}
		lookup = append(lookup, change)
		if from := since(change, now, retentionDays); oldest.IsZero() || from.Before(oldest) {
			oldest = from
		}
	}
	if len(lookup) == 0 {
		return out
	}
	days, err := invokelog.VerifyLines(vaultDir, checker, oldest.UTC().Format("2006-01-02"), "")
	if err != nil && checker != nil {
		// A check that cannot be made never passes a line as verified: the lines go unchecked instead.
		days, err = invokelog.VerifyLines(vaultDir, nil, oldest.UTC().Format("2006-01-02"), "")
	}
	for _, change := range lookup {
		if err != nil {
			out[change.Connection] = Origin{Source: OriginUnknown}
			continue
		}
		out[change.Connection] = originOf(change, days, configModTime, since(change, now, retentionDays))
	}
	return out
}

// originOf walks days from the youngest line back and stops at the first entry that explains change.
func originOf(change Change, days []invokelog.DayLines, configModTime, after time.Time) Origin {
	for i := len(days) - 1; i >= 0; i-- {
		lines := days[i].Lines
		for j := len(lines) - 1; j >= 0; j-- {
			line := lines[j]
			e := line.Entry
			if !line.Parsed || e.Kind != "" || e.Connection != change.Connection || e.Result != "success" ||
				e.Time.IsZero() || !e.Time.After(after) {
				continue
			}
			if e.Operation != invokelog.OperationConnectionCreate && e.Operation != invokelog.OperationConnectionChange {
				continue
			}
			if e.Path != OriginTUI && e.Path != OriginWeb && e.Path != OriginCLI {
				continue
			}
			return Origin{Source: e.Path, Time: e.Time, Seq: e.Seq,
				Verified: line.MAC == invokelog.MACValid && line.Chain == invokelog.ChainIntact}
		}
	}
	return Origin{Source: OriginOutside, Time: configModTime}
}

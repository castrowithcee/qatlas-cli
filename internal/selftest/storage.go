package selftest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

var storageScenario = Scenario{
	Name:    "storage",
	Summary: "reads under writes, lock wait of several writer processes, appends to the invoke log",
	Run:     runStorage,
}

func runStorage(ctx context.Context, env *Env) ([]Measurement, error) {
	p := env.Profile.Storage
	var out []Measurement

	// caseDir gives each case its own directory and removes it afterwards, so fixtures do not pile up.
	caseDir := func(name string) (string, func(), error) {
		dir := filepath.Join(env.Dir, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", nil, err
		}
		return dir, func() { _ = os.RemoveAll(dir) }, nil
	}

	for _, entries := range p.ReadEntries {
		for _, readers := range p.Readers {
			label := fmt.Sprintf("entries-%d/readers-%d", entries, readers)
			env.Progressf("read-under-write %s", label)
			dir, cleanup, err := caseDir("read")
			if err != nil {
				return nil, err
			}
			fx, err := NewStorageFixture(dir, entries)
			if err != nil {
				cleanup()
				return nil, err
			}
			res, err := fx.ReadUnderWrite(ctx, readers, p.ReadIterations)
			cleanup()
			if err != nil {
				return nil, err
			}
			out = append(out, Measurement{Group: "read-under-write", Case: label, Values: res.Values()})
		}
	}

	for _, entries := range p.LockEntries {
		for _, writers := range p.LockWriters {
			label := fmt.Sprintf("entries-%d/writers-%d", entries, writers)
			env.Progressf("lock-wait %s", label)
			dir, cleanup, err := caseDir("lock")
			if err != nil {
				return nil, err
			}
			fx, err := NewStorageFixture(dir, entries)
			if err != nil {
				cleanup()
				return nil, err
			}
			var rounds []LockWaitRound
			for i := 0; i < p.LockRounds; i++ {
				// Every round starts from the same fixture, so the file does not grow across rounds.
				if err = fx.Reset(); err != nil {
					break
				}
				var round LockWaitRound
				if round, err = RunLockWaitRound(ctx, env.Worker, fx.Path(), writers, nil); err != nil {
					break
				}
				rounds = append(rounds, round)
			}
			cleanup()
			if err != nil {
				return nil, err
			}
			out = append(out, Measurement{Group: "lock-wait", Case: label, Values: LockWaitValues(rounds)})
		}
	}

	for _, empty := range []bool{false, true} {
		for _, days := range p.AppendDays {
			label := fmt.Sprintf("days-%d", days)
			if empty {
				label += "-empty-newest"
			}
			env.Progressf("append %s", label)
			dir, cleanup, err := caseDir("append")
			if err != nil {
				return nil, err
			}
			fx, err := NewAppendFixture(dir, days, empty)
			if err != nil {
				cleanup()
				return nil, err
			}
			st, err := fx.Measure(ctx, p.AppendIterations)
			cleanup()
			if err != nil {
				return nil, err
			}
			out = append(out, Measurement{Group: "append", Case: label, Values: []Value{
				{"append-p50-ms", ms(st.P50)}, {"append-p95-ms", ms(st.P95)}, {"append-max-ms", ms(st.Max)},
			}})
		}
	}
	return out, nil
}

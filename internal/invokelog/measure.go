package invokelog

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// WithClock returns a logger like l that reads the time from now, so a measurement can pin the day an entry
// lands on. A nil now means the system clock.
func (l *Logger) WithClock(now func() time.Time) *Logger {
	out := *l
	out.now = now
	return &out
}

// SeedDayFiles writes days day files with one valid line each under vaultDir/logs, the newest dated newest
// (UTC) and the others on the days before. With emptyNewest the newest file exists but is empty, so the
// predecessor lookup of Append has to walk back one file. It exists to build synthetic fixtures for storage
// measurements and must only be pointed at a scratch directory.
func SeedDayFiles(vaultDir string, days int, emptyNewest bool, newest time.Time) error {
	dir := (&Logger{vaultDir: vaultDir}).logsDir()
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	newest = newest.UTC()
	for i := 0; i < days; i++ {
		name := newest.AddDate(0, 0, i-(days-1)).Format("2006-01-02") + ".jsonl"
		content := []byte(`{"seq":` + strconv.Itoa(i+1) + `,"operation":"selftest.seed"}` + "\n")
		if emptyNewest && i == days-1 {
			content = nil
		}
		if err := os.WriteFile(filepath.Join(dir, name), content, fileMode); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

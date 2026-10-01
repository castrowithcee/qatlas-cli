package selfupdate

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// OtherProcesses counts the qatlas processes of the current user besides this one, read through /proc. A
// failure is -1, never an error: the count only informs a question.
func OtherProcesses() int {
	return countProcesses("/proc", os.Getpid(), os.Getuid())
}

func countProcesses(root string, self, uid int) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		return -1
	}
	count := 0
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
			continue
		}
		comm, err := os.ReadFile(root + "/" + entry.Name() + "/comm")
		if err == nil && strings.TrimSpace(string(comm)) == "qatlas" {
			count++
		}
	}
	return count
}

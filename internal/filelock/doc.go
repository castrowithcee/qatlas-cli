// Package filelock provides an exclusive, blocking, whole-file lock that works the same way across
// processes on Linux, macOS and Windows. Callers lock a dedicated, stable lock file, never a file that is
// replaced by rename, because the lock belongs to the open file and not to the path.
package filelock

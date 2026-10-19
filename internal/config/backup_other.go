//go:build !windows

package config

import "os"

// restrictFile makes the mode explicit so an umask cannot widen it.
func restrictFile(path string) error { return os.Chmod(path, fileMode) }

func restrictDir(path string) error { return os.Chmod(path, dirMode) }

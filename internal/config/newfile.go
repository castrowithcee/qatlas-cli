package config

import "os"

// WriteNewPrivateFile creates path, which must not exist, readable and writable by this user alone (an ACL
// on Windows, like the configuration's own files), and fills it with data. It never replaces or follows
// anything at path: an existing file or link makes it fail with an error that satisfies
// errors.Is(err, fs.ErrExist). The access is restricted before data is written, and a file that could not be
// written completely is removed again.
func WriteNewPrivateFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return err
	}
	err = restrictFile(path)
	if err == nil {
		err = writeAndSync(f, data)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

// RedactDecodeError rebuilds an error of a YAML decoder so that it names the place and the kind of problem
// but never the text of the file that caused it (see Decode). A caller that decodes a YAML file of its own
// uses it before it shows the error.
func RedactDecodeError(err error) error { return redactDecodeError(err) }

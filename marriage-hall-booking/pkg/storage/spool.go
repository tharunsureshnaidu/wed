package storage

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SpoolDir holds bytes between the API accepting an upload and the worker
// putting it in S3.
//
// ponytail: a directory, not Redis or the database. The file is already on
// disk as a multipart temp file, the two processes run on the same host, and a
// filesystem stores blobs for free. If they are ever split across hosts this
// becomes a shared volume or a direct-to-S3 staging prefix - the event payload
// carries a path either way, so only this file changes.
var SpoolDir = "uploads/spool"

// Spool writes r to the spool directory and returns the path to hand the
// worker. The name is random for the same reason stored object keys are: an
// uploaded filename can contain path separators.
func Spool(r io.Reader, ext string, limit int64) (string, int64, error) {
	if err := os.MkdirAll(SpoolDir, 0o755); err != nil {
		return "", 0, err
	}
	name, err := randomName(ext)
	if err != nil {
		return "", 0, err
	}
	path := filepath.Join(SpoolDir, name)

	f, err := os.Create(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	n, err := io.Copy(f, io.LimitReader(r, limit))
	if err != nil {
		_ = f.Close()
		os.Remove(path) // never leave a truncated file for the worker to upload
		return "", 0, err
	}
	return path, n, nil
}

// Unspool removes a spooled file once it is safely in object storage. A missing
// file is not an error: a retried event whose first attempt already cleaned up
// must not fail the second time.
func Unspool(path string) error {
	if path == "" {
		return nil
	}
	// The path arrives in a Kafka message, and a message is data, not
	// something to trust with an unlink.
	if !InSpool(path) {
		return nil
	}
	if err := os.Remove(filepath.Clean(path)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// InSpool reports whether path names a file inside the spool directory. Both
// the worker's read and its unlink check it: the path comes from a message.
func InSpool(path string) bool {
	if path == "" {
		return false
	}
	rel, err := filepath.Rel(SpoolDir, filepath.Clean(path))
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !strings.HasPrefix(rel, "../")
}

package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpoolRoundTrip(t *testing.T) {
	dir := t.TempDir()
	defer swapSpoolDir(dir)()

	path, n, err := Spool(strings.NewReader("hello"), ".jpg", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("wrote %d bytes, want 5", n)
	}
	if !strings.HasSuffix(path, ".jpg") {
		t.Errorf("path %q lost its extension", path)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "hello" {
		t.Fatalf("read back %q, %v", b, err)
	}
	if err := Unspool(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file survived Unspool")
	}
	// A replayed event unspools a path that is already gone; that must succeed.
	if err := Unspool(path); err != nil {
		t.Errorf("second Unspool returned %v, want nil", err)
	}
}

// The path arrives in a Kafka message, which is data, not something to trust
// with an unlink.
func TestUnspoolRefusesPathsOutsideSpool(t *testing.T) {
	dir := t.TempDir()
	defer swapSpoolDir(filepath.Join(dir, "spool"))()
	if err := os.MkdirAll(SpoolDir, 0o755); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Unspool(filepath.Join(SpoolDir, "..", "secret.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("traversal escaped the spool directory and deleted a file outside it")
	}
}

// A write that fails partway must not leave a truncated file for the worker to
// upload as though it were complete.
func TestSpoolRemovesPartialFileOnError(t *testing.T) {
	dir := t.TempDir()
	defer swapSpoolDir(dir)()

	_, _, err := Spool(errReader{}, ".jpg", 1<<20)
	if err == nil {
		t.Fatal("expected an error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("left %d file(s) behind after a failed write", len(entries))
	}
}

// swapSpoolDir points SpoolDir at a temp directory for one test.
func swapSpoolDir(dir string) func() {
	prev := SpoolDir
	SpoolDir = dir
	return func() { SpoolDir = prev }
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, os.ErrClosed }

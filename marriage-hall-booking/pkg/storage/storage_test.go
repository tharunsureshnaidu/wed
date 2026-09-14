package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The key layout is the contract with Java's S3UploadService: objects written
// by either stack must land in the same place, or media uploaded before the
// migration becomes unreachable.
func TestKeyLayoutMatchesJava(t *testing.T) {
	for _, tc := range []struct {
		name string
		up   Upload
		want string
	}{
		{"vendor image", Upload{Kind: Image, VendorID: "v1", FacilityID: "f1"},
			"vendors/vendor-v1/venue-f1/gallery/X"},
		{"vendor video", Upload{Kind: Video, VendorID: "v1", FacilityID: "f1"},
			"vendors/vendor-v1/venue-f1/videos/X"},
		{"no vendor", Upload{Kind: Image, FacilityID: "f1"},
			"venues/f1/gallery/X"},
		{"document", Upload{Kind: Document},
			"documents/X"},
	} {
		if got := keyFor(tc.up, "X"); got != tc.want {
			t.Errorf("%s: key = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A stored URL is data read back from the database. Treating its tail as a path
// must never let it address a file outside the upload directory.
func TestLocalDeleteRefusesTraversal(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	l := NewLocal(sub, "http://localhost:8080")

	if err := l.Delete(context.Background(),
		"http://localhost:8080/uploads/../secret.txt"); err != nil {
		t.Fatalf("delete returned %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("traversal escaped the upload directory and deleted a file outside it")
	}
}

func TestLocalPutAndDelete(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(dir, "http://localhost:8080/")
	body := strings.NewReader("hello")

	res, err := l.Put(context.Background(), Upload{
		Kind: Image, Body: body, Size: 5, ContentType: "image/png", Ext: ".png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.URL, "http://localhost:8080/uploads/") {
		t.Errorf("URL = %q", res.URL)
	}
	if res.Size != 5 {
		t.Errorf("size = %d, want 5", res.Size)
	}
	if _, err := os.Stat(filepath.Join(dir, res.Key)); err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if err := l.Delete(context.Background(), res.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, res.Key)); !os.IsNotExist(err) {
		t.Error("file still present after delete")
	}
}

// Oversized uploads are refused before any bytes are written.
func TestLimitsPerKind(t *testing.T) {
	l := NewLocal(t.TempDir(), "http://x")
	_, err := l.Put(context.Background(), Upload{
		Kind: Image, Body: strings.NewReader(""), Size: MaxImageBytes + 1, Ext: ".png",
	})
	if _, ok := err.(ErrTooLarge); !ok {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if Limit(Video) != MaxVideoBytes || Limit(Document) != MaxDocumentBytes {
		t.Error("per-kind limits do not match Java's")
	}
}

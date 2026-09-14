package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Local writes media to a directory served by the API itself.
//
// ponytail: development only. It needs no AWS credentials, but anything written
// here is lost when the container is replaced - which is why New() picks S3
// whenever a bucket is configured, and why Name() is surfaced on /health.
type Local struct {
	dir     string
	baseURL string
}

// NewLocal stores under dir and serves from baseURL + "/uploads/".
func NewLocal(dir, baseURL string) *Local {
	return &Local{dir: dir, baseURL: trimSlash(baseURL)}
}

func (l *Local) Name() string { return "local:" + l.dir }

func (l *Local) Put(ctx context.Context, up Upload) (Result, error) {
	if limit := Limit(up.Kind); up.Size > limit {
		return Result{}, ErrTooLarge{Kind: up.Kind, Limit: limit}
	}
	name, err := randomName(up.Ext)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return Result{}, err
	}
	dst, err := os.Create(filepath.Join(l.dir, name))
	if err != nil {
		return Result{}, err
	}
	defer dst.Close()

	n, err := io.Copy(dst, io.LimitReader(up.Body, up.Size))
	if err != nil {
		return Result{}, err
	}
	return Result{URL: l.baseURL + "/uploads/" + name, Key: name, Size: n}, nil
}

// Delete removes a file this store wrote. A URL from anywhere else is ignored,
// and the filename is taken as a base name only - a stored URL must never be
// able to address a path outside the upload directory.
func (l *Local) Delete(ctx context.Context, url string) error {
	i := strings.LastIndex(url, "/uploads/")
	if i < 0 {
		return nil
	}
	name := filepath.Base(url[i+len("/uploads/"):])
	if name == "." || name == "/" || name == ".." {
		return nil
	}
	err := os.Remove(filepath.Join(l.dir, name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

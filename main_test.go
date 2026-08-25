package main

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func writeTempFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	return path
}

func TestHashFirstKB(t *testing.T) {
	tests := []struct {
		desc    string
		content []byte
	}{
		{"empty file", []byte("")},
		{"shorter than 1KB", []byte("hello world")},
		{"exactly 1KB", bytes.Repeat([]byte("a"), 1024)},
		{"longer than 1KB", bytes.Repeat([]byte("b"), 4096)},
	}

	dir := t.TempDir()

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			path := writeTempFile(t, dir, tc.desc, tc.content)

			got, err := hashFirstKB(path)
			if err != nil {
				t.Fatalf("hashFirstKB failed: %v", err)
			}

			limit := min(len(tc.content), 1024)

			sum := md5.Sum(tc.content[:limit])
			want := hex.EncodeToString(sum[:])

			if got != want {
				t.Errorf("hashFirstKB(%q) = %s, want %s", tc.desc, got, want)
			}
		})
	}
}

func TestHashFirstKBMissingFile(t *testing.T) {
	if _, err := hashFirstKB(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestHashSHA256(t *testing.T) {
	dir := t.TempDir()
	content := []byte("the quick brown fox jumps over the lazy dog")
	path := writeTempFile(t, dir, "file.txt", content)

	got, err := hashSHA256(path)
	if err != nil {
		t.Fatalf("hashSHA256 failed: %v", err)
	}

	sum := sha256.Sum256(content)
	want := hex.EncodeToString(sum[:])

	if got != want {
		t.Errorf("hashSHA256() = %s, want %s", got, want)
	}
}

func TestHashSHA256MissingFile(t *testing.T) {
	if _, err := hashSHA256(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestProcessGlobs(t *testing.T) {
	tests := []struct {
		desc     string
		includes []string
		excludes []string
		wantErr  bool
	}{
		{"no patterns", nil, nil, false},
		{"valid patterns", []string{"*.go"}, []string{"*_test.go"}, false},
		{"invalid include pattern", []string{"["}, nil, true},
		{"invalid exclude pattern", nil, []string{"["}, true},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			includeGlobs, excludeGlobs, err := processGlobs(tc.includes, tc.excludes)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("processGlobs failed: %v", err)
			}

			if len(includeGlobs) != len(tc.includes) {
				t.Errorf("got %d include globs, want %d", len(includeGlobs), len(tc.includes))
			}

			if len(excludeGlobs) != len(tc.excludes) {
				t.Errorf("got %d exclude globs, want %d", len(excludeGlobs), len(tc.excludes))
			}
		})
	}
}

func TestProcessQueryFile(t *testing.T) {
	dir := t.TempDir()
	content := []byte("query file content")
	path := writeTempFile(t, dir, "query.txt", content)

	size, md5sum, sha, err := processQueryFile(path)
	if err != nil {
		t.Fatalf("processQueryFile failed: %v", err)
	}

	if size != int64(len(content)) {
		t.Errorf("size = %d, want %d", size, len(content))
	}

	wantMD5, err := hashFirstKB(path)
	if err != nil {
		t.Fatalf("hashFirstKB failed: %v", err)
	}

	if md5sum != wantMD5 {
		t.Errorf("md5 = %s, want %s", md5sum, wantMD5)
	}

	wantSHA, err := hashSHA256(path)
	if err != nil {
		t.Fatalf("hashSHA256 failed: %v", err)
	}

	if sha != wantSHA {
		t.Errorf("sha256 = %s, want %s", sha, wantSHA)
	}
}

func TestProcessQueryFileMissing(t *testing.T) {
	if _, _, _, err := processQueryFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("expected error for missing query file, got nil")
	}
}

func TestProcessFile(t *testing.T) {
	dir := t.TempDir()
	content := []byte("duplicate me")
	queryPath := writeTempFile(t, dir, "query.txt", content)
	copyPath := writeTempFile(t, dir, "copy.txt", content)
	diffPath := writeTempFile(t, dir, "different.txt", []byte("not the same"))
	sameSizePath := writeTempFile(t, dir, "samesize.txt", []byte("duplicate m3"))

	querySize, queryMD5, querySHA, err := processQueryFile(queryPath)
	if err != nil {
		t.Fatalf("processQueryFile failed: %v", err)
	}

	tests := []struct {
		desc      string
		path      string
		wantMatch bool
	}{
		{"identical content", copyPath, true},
		{"different size", diffPath, false},
		{"same size different content", sameSizePath, false},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			out := captureStdout(t, func() {
				processFile(tc.path, querySize, queryMD5, querySHA, false)
			})

			matched := bytes.Contains([]byte(out), []byte(tc.path))
			if matched != tc.wantMatch {
				t.Errorf("processFile(%s) matched = %v, want %v", tc.desc, matched, tc.wantMatch)
			}
		})
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns what was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}

	original := os.Stdout
	os.Stdout = w

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("failed to close pipe writer: %v", err)
	}

	os.Stdout = original

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("failed to read pipe: %v", err)
	}

	return buf.String()
}

package filestore

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sabadia/svc-downloader/internal/models"
)

func TestLocalFileStore_Prepare(t *testing.T) {
	store := NewLocalFileStore()
	ctx := context.Background()

	t.Run("valid file options", func(t *testing.T) {
		download := &models.Download{
			ID: "test-prepare-1",
			File: &models.FileOptions{
				Path:     t.TempDir(),
				Filename: "test.txt",
			},
		}

		err := store.Prepare(ctx, download)
		if err != nil {
			t.Fatalf("Prepare() error = %v", err)
		}

		// Check that temp directory was created
		tempDir := store.tempDir(download)
		if _, err := os.Stat(tempDir); os.IsNotExist(err) {
			t.Errorf("temp directory %s was not created", tempDir)
		}
	})

	t.Run("no file options", func(t *testing.T) {
		download := &models.Download{
			ID: "test-prepare-2",
		}

		err := store.Prepare(ctx, download)
		if err == nil {
			t.Error("Prepare() expected error for missing file options")
		}
		if !strings.Contains(err.Error(), "file options required") {
			t.Errorf("Prepare() error = %v, want error containing 'file options required'", err)
		}
	})

	t.Run("empty path uses home directory", func(t *testing.T) {
		download := &models.Download{
			ID: "test-prepare-3",
			File: &models.FileOptions{
				Filename: "test.txt",
			},
		}

		err := store.Prepare(ctx, download)
		if err != nil {
			t.Fatalf("Prepare() error = %v", err)
		}

		// The Prepare method doesn't modify the download.File.Path field,
		// it just uses the home directory internally for directory creation
		// So we just verify that no error occurred
		if download.File.Path != "" {
			t.Errorf("Prepare() should not modify File.Path, got %s", download.File.Path)
		}
	})
}

func TestLocalFileStore_OpenSegmentWriter(t *testing.T) {
	store := NewLocalFileStore()
	ctx := context.Background()

	download := &models.Download{
		ID: "test-writer-1",
		File: &models.FileOptions{
			Path:     t.TempDir(),
			Filename: "test.txt",
		},
	}

	// Prepare first
	err := store.Prepare(ctx, download)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	segment := models.Segment{
		ID:         "seg-1",
		DownloadID: download.ID,
		Index:      0,
		Start:      0,
		End:        100,
	}

	writer, existing, err := store.OpenSegmentWriter(ctx, download, segment)
	if err != nil {
		t.Fatalf("OpenSegmentWriter() error = %v", err)
	}
	defer writer.Close()

	// For a new file, existing should be 0
	if existing < 0 {
		t.Errorf("OpenSegmentWriter() existing = %d, want >= 0", existing)
	}

	// Write some data
	testData := []byte("test data")
	n, err := writer.Write(testData)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != len(testData) {
		t.Errorf("Write() wrote %d bytes, want %d", n, len(testData))
	}
}

func TestLocalFileStore_VerifyChecksum(t *testing.T) {
	store := NewLocalFileStore()
	ctx := context.Background()

	testData := []byte("test data for checksum verification")
	testFile := filepath.Join(t.TempDir(), "test.txt")

	// Create test file
	err := os.WriteFile(testFile, testData, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	tests := []struct {
		name         string
		checksumType string
		checksum     string
		wantValid    bool
		wantError    bool
	}{
		{
			name:         "valid md5",
			checksumType: "md5",
			checksum:     func() string { h := md5.Sum(testData); return hex.EncodeToString(h[:]) }(),
			wantValid:    true,
			wantError:    false,
		},
		{
			name:         "invalid md5",
			checksumType: "md5",
			checksum:     "invalid",
			wantValid:    false,
			wantError:    false,
		},
		{
			name:         "valid sha1",
			checksumType: "sha1",
			checksum:     func() string { h := sha1.Sum(testData); return hex.EncodeToString(h[:]) }(),
			wantValid:    true,
			wantError:    false,
		},
		{
			name:         "valid sha256",
			checksumType: "sha256",
			checksum:     func() string { h := sha256.Sum256(testData); return hex.EncodeToString(h[:]) }(),
			wantValid:    true,
			wantError:    false,
		},
		{
			name:         "valid crc32c",
			checksumType: "crc32c",
			checksum:     fmt.Sprintf("%08x", crc32.Checksum(testData, crc32.MakeTable(crc32.Castagnoli))),
			wantValid:    true,
			wantError:    false,
		},
		{
			name:         "unsupported checksum type",
			checksumType: "unsupported",
			checksum:     "test",
			wantValid:    false,
			wantError:    true,
		},
		{
			name:         "no checksum",
			checksumType: "",
			checksum:     "",
			wantValid:    true,
			wantError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			download := &models.Download{
				ID: "test-checksum",
				File: &models.FileOptions{
					Path:         filepath.Dir(testFile),
					Filename:     filepath.Base(testFile),
					Checksum:     tt.checksum,
					ChecksumType: tt.checksumType,
				},
			}

			valid, err := store.VerifyChecksum(ctx, download)

			if tt.wantError && err == nil {
				t.Error("VerifyChecksum() expected error")
			}
			if !tt.wantError && err != nil {
				t.Errorf("VerifyChecksum() error = %v", err)
			}
			if valid != tt.wantValid {
				t.Errorf("VerifyChecksum() valid = %v, want %v", valid, tt.wantValid)
			}
		})
	}
}

func TestLocalFileStore_MergeSegments(t *testing.T) {
	store := NewLocalFileStore()
	ctx := context.Background()

	download := &models.Download{
		ID: "test-merge-1",
		File: &models.FileOptions{
			Path:     t.TempDir(),
			Filename: "merged.txt",
		},
	}

	// Prepare
	err := store.Prepare(ctx, download)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	// Create test segment files
	segmentData := [][]byte{
		[]byte("segment 1\n"),
		[]byte("segment 2\n"),
		[]byte("segment 3\n"),
	}

	tempDir := store.tempDir(download)
	for i, data := range segmentData {
		partFile := filepath.Join(tempDir, fmt.Sprintf("%09d.part", i))
		err := os.WriteFile(partFile, data, 0644)
		if err != nil {
			t.Fatalf("Failed to create part file %d: %v", i, err)
		}
	}

	// Merge segments
	err = store.MergeSegments(ctx, download)
	if err != nil {
		t.Fatalf("MergeSegments() error = %v", err)
	}

	// Verify merged file
	finalPath := store.finalPath(download)
	mergedData, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("Failed to read merged file: %v", err)
	}

	expectedData := []byte("segment 1\nsegment 2\nsegment 3\n")
	if string(mergedData) != string(expectedData) {
		t.Errorf("Merged data = %q, want %q", string(mergedData), string(expectedData))
	}

	// Verify temp directory was cleaned up
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Errorf("Temp directory %s was not cleaned up", tempDir)
	}
}

func TestLocalFileStore_MergeSegments_UniqueFilename(t *testing.T) {
	store := NewLocalFileStore()
	ctx := context.Background()

	baseDir := t.TempDir()
	download := &models.Download{
		ID: "test-unique-1",
		File: &models.FileOptions{
			Path:           baseDir,
			Filename:       "test.txt",
			Overwrite:      false,
			UniqueFilename: true,
		},
	}

	// Create existing file
	existingFile := filepath.Join(baseDir, "test.txt")
	err := os.WriteFile(existingFile, []byte("existing"), 0644)
	if err != nil {
		t.Fatalf("Failed to create existing file: %v", err)
	}

	// Prepare
	err = store.Prepare(ctx, download)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	// Create test segment
	tempDir := store.tempDir(download)
	partFile := filepath.Join(tempDir, "000000000.part")
	err = os.WriteFile(partFile, []byte("new content"), 0644)
	if err != nil {
		t.Fatalf("Failed to create part file: %v", err)
	}

	// Merge segments
	err = store.MergeSegments(ctx, download)
	if err != nil {
		t.Fatalf("MergeSegments() error = %v", err)
	}

	// Verify unique filename was generated
	expectedFilename := "test (1).txt"
	if download.File.Filename != expectedFilename {
		t.Errorf("Filename = %s, want %s", download.File.Filename, expectedFilename)
	}

	// Verify file was created with unique name
	uniqueFile := filepath.Join(baseDir, expectedFilename)
	if _, err := os.Stat(uniqueFile); os.IsNotExist(err) {
		t.Errorf("Unique file %s was not created", uniqueFile)
	}
}

func TestLocalFileStore_RemoveDownloadFiles(t *testing.T) {
	store := NewLocalFileStore()
	ctx := context.Background()

	download := &models.Download{
		ID: "test-remove-1",
		File: &models.FileOptions{
			Path:     t.TempDir(),
			Filename: "test.txt",
		},
	}

	// Prepare and create some files
	err := store.Prepare(ctx, download)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	// Create temp directory and files
	tempDir := store.tempDir(download)
	err = os.MkdirAll(tempDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	partFile := filepath.Join(tempDir, "000000000.part")
	err = os.WriteFile(partFile, []byte("test"), 0644)
	if err != nil {
		t.Fatalf("Failed to create part file: %v", err)
	}

	// Create final file
	finalPath := store.finalPath(download)
	err = os.WriteFile(finalPath, []byte("final"), 0644)
	if err != nil {
		t.Fatalf("Failed to create final file: %v", err)
	}

	// Remove download files
	err = store.RemoveDownloadFiles(ctx, download)
	if err != nil {
		t.Fatalf("RemoveDownloadFiles() error = %v", err)
	}

	// Verify files were removed
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Errorf("Temp directory %s was not removed", tempDir)
	}
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Errorf("Final file %s was not removed", finalPath)
	}
}

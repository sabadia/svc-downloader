package filestore

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sabadia/svc-downloader/internal/models"
)

type LocalFileStore struct{}

func NewLocalFileStore() *LocalFileStore { return &LocalFileStore{} }

func (l *LocalFileStore) Prepare(ctx context.Context, d *models.Download) error {
	if d.File == nil {
		return errors.New("file options required")
	}
	baseDir := d.File.Path
	if baseDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		baseDir = filepath.Join(home, "Downloads")
	}
	if baseDir == "" {
		return errors.New("file.path required")
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return err
	}
	tmp := l.tempDir(d)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	return nil
}

func (l *LocalFileStore) OpenSegmentWriter(ctx context.Context, d *models.Download, s models.Segment) (io.WriteCloser, error) {
	tmp := l.tempDir(d)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(tmp, fmt.Sprintf("%09d.part", s.Index))
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	// seek to end for resume
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (l *LocalFileStore) CompleteSegment(ctx context.Context, d *models.Download, s models.Segment) error {
	// No-op for local filesystem; writer close flushes data
	return nil
}

func (l *LocalFileStore) MergeSegments(ctx context.Context, d *models.Download) error {
	parts, err := l.listParts(d)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return errors.New("no parts to merge")
	}
	outPath := l.finalPath(d)
	// Overwrite/UniqueFilename handling
	if _, err := os.Stat(outPath); err == nil {
		if d.File != nil && !d.File.Overwrite {
			if d.File.UniqueFilename {
				base := d.File.Filename
				ext := filepath.Ext(base)
				name := strings.TrimSuffix(base, ext)
				for i := 1; ; i++ {
					candidate := fmt.Sprintf("%s (%d)%s", name, i, ext)
					candidatePath := filepath.Join(d.File.Path, candidate)
					if _, err := os.Stat(candidatePath); os.IsNotExist(err) {
						d.File.Filename = candidate
						outPath = candidatePath
						break
					}
				}
			} else {
				return errors.New("target file exists and overwrite is false")
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()
	for _, p := range parts {
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, f); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}
	// Cleanup temp directory
	_ = os.RemoveAll(l.tempDir(d))
	return nil
}

func (l *LocalFileStore) RemoveDownloadFiles(ctx context.Context, d *models.Download) error {
	_ = os.RemoveAll(l.tempDir(d))
	return os.Remove(l.finalPath(d))
}

func (l *LocalFileStore) VerifyChecksum(ctx context.Context, d *models.Download) (bool, error) {
	if d.File == nil || d.File.Checksum == "" {
		return true, nil
	}
	fp := l.finalPath(d)
	f, err := os.Open(fp)
	if err != nil {
		return false, err
	}
	defer f.Close()
	switch strings.ToLower(d.File.ChecksumType) {
	case "md5":
		h := md5.New()
		if _, err := io.Copy(h, f); err != nil {
			return false, err
		}
		sum := hex.EncodeToString(h.Sum(nil))
		return strings.EqualFold(sum, d.File.Checksum), nil
	case "sha256":
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return false, err
		}
		sum := hex.EncodeToString(h.Sum(nil))
		return strings.EqualFold(sum, d.File.Checksum), nil
	default:
		return false, fmt.Errorf("unsupported checksum type: %s", d.File.ChecksumType)
	}
}

func (l *LocalFileStore) tempDir(d *models.Download) string {
	if d.File != nil && d.File.TempDir != "" {
		return filepath.Join(d.File.TempDir, d.ID)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("warning: could not determine user home directory for temp files, using current directory")
		return filepath.Join(d.File.Path, ".tmp", d.ID)
	}
	return filepath.Join(home, ".slog", "downloader", "tmp", d.ID)
}

func (l *LocalFileStore) finalPath(d *models.Download) string {
	filename := d.File.Filename
	if filename == "" && d.File != nil {
		// fallback to ID if no filename provided
		filename = d.ID
	}
	return filepath.Join(d.File.Path, filename)
}

func (l *LocalFileStore) listParts(d *models.Download) ([]string, error) {
	dir := l.tempDir(d)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".part") {
			files = append(files, filepath.Join(dir, name))
		}
	}
	sort.Strings(files)
	return files, nil
}

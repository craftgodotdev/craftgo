// User-owned helpers shared by the MediaService handlers: reading the actual
// bytes of an uploaded part, persisting them, and minting ids.
package mediaservice

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"time"

	types "github.com/craftgodotdev/craftgo/example/upload/internal/types/media"
	"github.com/craftgodotdev/craftgo/example/upload/svccontext"
)

// uploadDir is where this example persists received files (relative to the
// server's working directory). A real service would stream to object storage
// (S3 / GCS) instead.
const uploadDir = "uploads"

// newID mints a short random id with a readable prefix, e.g. "med_3f9a1c…".
func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

// contentType returns the uploaded part's declared Content-Type, or a generic
// fallback when the client sent none.
func contentType(fh *multipart.FileHeader) string {
	if ct := fh.Header.Get("Content-Type"); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// saveUpload reads the ACTUAL bytes of an uploaded part and writes them to
// ./uploads. `fh.Open()` returns a reader over the file's real content (the
// FileHeader itself is only metadata); io.Copy streams that content to the
// destination file AND a SHA-256 hasher at once, so we persist the bytes and
// fingerprint them in a single pass without buffering the whole file in memory.
// Returns the byte count actually received, the content hash, and the stored
// filename.
func saveUpload(id string, fh *multipart.FileHeader) (size int, sha, stored string, err error) {
	src, err := fh.Open() // reader over the real uploaded bytes
	if err != nil {
		return 0, "", "", fmt.Errorf("open upload %q: %w", fh.Filename, err)
	}
	defer src.Close()

	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		return 0, "", "", fmt.Errorf("mkdir %s: %w", uploadDir, err)
	}
	stored = id + "-" + filepath.Base(fh.Filename) // filepath.Base defuses path traversal
	dst, err := os.Create(filepath.Join(uploadDir, stored))
	if err != nil {
		return 0, "", "", fmt.Errorf("create %s: %w", stored, err)
	}
	defer dst.Close()

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), src) // bytes → disk AND hash
	if err != nil {
		return 0, "", "", fmt.Errorf("save upload %q: %w", fh.Filename, err)
	}
	return int(n), hex.EncodeToString(h.Sum(nil)), stored, nil
}

// storeUpload reads + persists one uploaded file and records it as a media
// item. Shared by the single-file endpoints (avatar / document / attachment).
func storeUpload(store *svccontext.MediaStore, fh *multipart.FileHeader) (*types.UploadResult, error) {
	id := newID("med")
	size, sha, _, err := saveUpload(id, fh)
	if err != nil {
		return nil, err
	}
	res := &types.UploadResult{
		ID:        id,
		SizeBytes: size,
		Sha256:    sha,
		MimeType:  contentType(fh),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	res.URL = "https://cdn.example.com/media/" + id
	store.PutMedia(res)
	return res, nil
}

// galleryPhoto reads + persists one uploaded photo and builds its metadata.
func galleryPhoto(fh *multipart.FileHeader) (types.GalleryPhoto, error) {
	id := newID("pho")
	size, sha, _, err := saveUpload(id, fh)
	if err != nil {
		return types.GalleryPhoto{}, err
	}
	return types.GalleryPhoto{
		ID:        id,
		Filename:  fh.Filename,
		SizeBytes: size,
		Sha256:    sha,
		MimeType:  contentType(fh),
	}, nil
}

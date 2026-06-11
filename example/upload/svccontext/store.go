// User-owned. The example's in-memory media store.
package svccontext

import (
	"sync"

	media "github.com/craftgodotdev/craftgo/example/upload/internal/types/media"
)

// MediaStore holds uploaded media and galleries in memory. A real service
// would stream blobs to object storage (S3 / GCS) and keep metadata in a
// database; the handlers depend only on these methods, so swapping the
// backing is a local change. Every method is safe for concurrent use.
type MediaStore struct {
	mu        sync.RWMutex
	media     map[string]*media.UploadResult
	galleries map[string]*media.Gallery
}

// NewMediaStore returns an empty store.
func NewMediaStore() *MediaStore {
	return &MediaStore{
		media:     map[string]*media.UploadResult{},
		galleries: map[string]*media.Gallery{},
	}
}

// PutMedia records (or replaces) a media item by id.
func (s *MediaStore) PutMedia(m *media.UploadResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.media[m.ID] = m
}

// GetMedia returns the media item for id; ok is false when absent.
func (s *MediaStore) GetMedia(id string) (m *media.UploadResult, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok = s.media[id]
	return m, ok
}

// DeleteMedia removes a media item; absent ids are a no-op (delete is
// idempotent).
func (s *MediaStore) DeleteMedia(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.media, id)
}

// PutGallery records (or replaces) a gallery by id.
func (s *MediaStore) PutGallery(g *media.Gallery) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.galleries[g.ID] = g
}

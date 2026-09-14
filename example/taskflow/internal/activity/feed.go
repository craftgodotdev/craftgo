// Package activity is the projection the event consumers build: a
// per-project feed of what happened, assembled entirely from published
// contracts rather than from reads against the task store.
package activity

import "sync"

// Entry is one line of a project's feed.
type Entry struct {
	ProjectID string
	TaskID    string
	Summary   string
	At        string
}

// Feed is an in-memory activity log keyed by project.
type Feed struct {
	mu      sync.RWMutex
	entries map[string][]Entry
}

// NewFeed returns an empty feed.
func NewFeed() *Feed { return &Feed{entries: map[string][]Entry{}} }

// Append records one entry against its project.
func (f *Feed) Append(e Entry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[e.ProjectID] = append(f.entries[e.ProjectID], e)
}

// For returns a project's entries in arrival order. Deliveries of
// different contracts race, so this is the order the feed saw them, not
// the order the events happened in.
func (f *Feed) For(projectID string) []Entry {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]Entry, len(f.entries[projectID]))
	copy(out, f.entries[projectID])
	return out
}

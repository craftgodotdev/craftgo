// Package store is Taskflow's persistence layer.
//
// This reference build ships a thread-safe IN-MEMORY implementation so the app
// runs with zero external dependencies: `go run .`, `docker run`, or the test
// suite all work without a database. To back it with Postgres, keep this method
// set and swap the map operations for SQL - a starter schema lives in
// migrations/0001_init.sql, and ServiceContext depends only on *Store, so no
// other layer changes.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	admin "github.com/craftgodotdev/craftgo/example/taskflow/internal/types/admin"
	attachments "github.com/craftgodotdev/craftgo/example/taskflow/internal/types/attachments"
	project "github.com/craftgodotdev/craftgo/example/taskflow/internal/types/project"
	shared "github.com/craftgodotdev/craftgo/example/taskflow/internal/types/shared"
	tasks "github.com/craftgodotdev/craftgo/example/taskflow/internal/types/tasks"
)

// Store holds every resource in memory behind a single RWMutex.
type Store struct {
	mu          sync.RWMutex
	projects    map[shared.ID]*project.Project
	projectsV2  map[shared.ID]*project.ProjectV2
	tasks       map[shared.ID]*tasks.Task
	comments    map[shared.ID][]tasks.Comment
	attachments map[shared.ID][]attachments.Attachment
	tokens      map[shared.ID]*admin.ApiToken
}

// New returns an empty, ready-to-use store.
func New() *Store {
	return &Store{
		projects:    map[shared.ID]*project.Project{},
		projectsV2:  map[shared.ID]*project.ProjectV2{},
		tasks:       map[shared.ID]*tasks.Task{},
		comments:    map[shared.ID][]tasks.Comment{},
		attachments: map[shared.ID][]attachments.Attachment{},
		tokens:      map[shared.ID]*admin.ApiToken{},
	}
}

// Ping reports store health for the readiness probe. The in-memory store is
// always ready; a Postgres store would `SELECT 1` here.
func (s *Store) Ping() error { return nil }

// ---- helpers ----

func newID() shared.ID {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return shared.ID(hex.EncodeToString(b[:]))
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func stamps() shared.Timestamps { t := now(); return shared.Timestamps{CreatedAt: t, UpdatedAt: t} }

func limitOf(p *int) int {
	if p == nil || *p <= 0 {
		return 20
	}
	return *p
}

// paginate applies a naive first-N page over an already-filtered, sorted slice.
// A production store would translate the opaque cursor into a keyset predicate.
func paginate[T any](items []T, limit *int) *shared.Page[T] {
	total := len(items)
	if n := limitOf(limit); n < total {
		items = items[:n]
	}
	if items == nil {
		items = []T{}
	}
	return &shared.Page[T]{Items: items, NextCursor: nil, Total: total}
}

func notFound(resource string, id shared.ID) error {
	return shared.NewResourceNotFoundErr(shared.ResourceNotFoundBody{Resource: resource, ID: id})
}

// ---- projects (v1) ----

func (s *Store) CreateProject(req *project.CreateProjectReq) (*project.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.projects {
		if p.Key == req.Key {
			return nil, shared.NewAlreadyExistsErr(shared.AlreadyExistsBody{Resource: "project", Field: "key"})
		}
	}
	p := &project.Project{
		ID:         newID(),
		Key:        req.Key,
		Name:       req.Name,
		Color:      req.Color,
		Status:     project.ProjectStatusActive,
		TaskCount:  0,
		Timestamps: stamps(),
	}
	s.projects[p.ID] = p
	return p, nil
}

func (s *Store) GetProject(id shared.ID) (*project.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projects[id]
	if !ok {
		return nil, notFound("project", id)
	}
	return p, nil
}

func (s *Store) ListProjects(req *project.ListProjectsReq) *shared.Page[project.Project] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []project.Project{}
	for _, p := range s.projects {
		if req.Status != nil && p.Status != *req.Status {
			continue
		}
		if req.Q != nil && !strings.Contains(strings.ToLower(p.Name), strings.ToLower(*req.Q)) {
			continue
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return paginate(out, req.Limit)
}

func (s *Store) UpdateProject(req *project.UpdateProjectReq) (*project.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[req.ID]
	if !ok {
		return nil, notFound("project", req.ID)
	}
	if req.Name != nil {
		p.Name = *req.Name
	}
	if req.Color != nil {
		p.Color = req.Color
	}
	p.UpdatedAt = now()
	return p, nil
}

func (s *Store) ArchiveProject(id shared.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return notFound("project", id)
	}
	p.Status = project.ProjectStatusArchived
	p.UpdatedAt = now()
	return nil
}

func (s *Store) projectExists(id shared.ID) bool {
	_, ok := s.projects[id]
	return ok
}

// ---- projects (v2) ----

func (s *Store) CreateProjectV2(req *project.CreateProjectV2Req) (*project.ProjectV2, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.projectsV2 {
		if p.Key == req.Key {
			return nil, shared.NewAlreadyExistsErr(shared.AlreadyExistsBody{Resource: "project", Field: "key"})
		}
	}
	p := &project.ProjectV2{
		ID:          newID(),
		Key:         req.Key,
		Name:        req.Name,
		Description: req.Description,
		OwnerID:     req.OwnerID,
		Color:       req.Color,
		Status:      project.ProjectStatusActive,
		TaskCount:   0,
		Timestamps:  stamps(),
	}
	s.projectsV2[p.ID] = p
	return p, nil
}

func (s *Store) GetProjectV2(id shared.ID) (*project.ProjectV2, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projectsV2[id]
	if !ok {
		return nil, notFound("project", id)
	}
	return p, nil
}

func (s *Store) ListProjectsV2(req *project.ListProjectsV2Req) *shared.Page[project.ProjectV2] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []project.ProjectV2{}
	for _, p := range s.projectsV2 {
		if req.OwnerID != nil && p.OwnerID != *req.OwnerID {
			continue
		}
		if req.Status != nil && p.Status != *req.Status {
			continue
		}
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return paginate(out, req.Limit)
}

// ---- tasks ----

func (s *Store) CreateTask(req *tasks.CreateTaskReq) (*tasks.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.projectExists(req.ProjectID) {
		return nil, notFound("project", req.ProjectID)
	}
	t := s.newTask(req.ProjectID, req.Title, req.Priority, req.Description, req.Points, req.AssigneeIds, req.Labels, req.DueAt)
	return t, nil
}

// newTask must be called with s.mu held.
func (s *Store) newTask(projectID shared.ID, title string, prio *tasks.Priority, desc *string, points *shared.Points, assignees []shared.ID, labels []string, dueAt *string) *tasks.Task {
	if assignees == nil {
		assignees = []shared.ID{}
	}
	if labels == nil {
		labels = []string{}
	}
	// @default(Medium) makes priority optional; the transport pre-fills it, but
	// default defensively to Medium if a caller passes nil.
	priority := tasks.PriorityMedium
	if prio != nil {
		priority = *prio
	}
	t := &tasks.Task{
		ID:          newID(),
		ProjectID:   projectID,
		Title:       title,
		Description: desc,
		Status:      tasks.TaskStatusTodo,
		Priority:    priority,
		Points:      points,
		ProgressPct: 0,
		AssigneeIds: assignees,
		Labels:      labels,
		DueAt:       dueAt,
		SearchText:  strings.ToLower(title),
		Timestamps:  stamps(),
	}
	s.tasks[t.ID] = t
	if p, ok := s.projects[projectID]; ok {
		p.TaskCount++
	}
	return t
}

func (s *Store) BulkCreateTasks(req *tasks.BulkCreateReq) (*shared.Page[tasks.Task], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.projectExists(req.ProjectID) {
		return nil, notFound("project", req.ProjectID)
	}
	out := make([]tasks.Task, 0, len(req.Tasks))
	for _, item := range req.Tasks {
		t := s.newTask(req.ProjectID, item.Title, item.Priority, nil, nil, nil, nil, nil)
		out = append(out, *t)
	}
	return &shared.Page[tasks.Task]{Items: out, Total: len(out)}, nil
}

func (s *Store) GetTask(projectID, id shared.ID) (*tasks.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok || t.ProjectID != projectID {
		return nil, notFound("task", id)
	}
	return t, nil
}

func (s *Store) ListTasks(req *tasks.ListTasksReq) (*shared.Page[tasks.Task], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.projectExists(req.ProjectID) {
		return nil, notFound("project", req.ProjectID)
	}
	out := []tasks.Task{}
	for _, t := range s.tasks {
		if t.ProjectID != req.ProjectID {
			continue
		}
		if req.Status != nil && t.Status != *req.Status {
			continue
		}
		if req.Priority != nil && t.Priority != *req.Priority {
			continue
		}
		if req.Assignee != nil && !slices.Contains(t.AssigneeIds, *req.Assignee) {
			continue
		}
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return paginate(out, req.Limit), nil
}

func (s *Store) SetTaskStatus(projectID, id shared.ID, status tasks.TaskStatus) (*tasks.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.ProjectID != projectID {
		return nil, notFound("task", id)
	}
	t.Status = status
	if status == tasks.TaskStatusDone {
		t.ProgressPct = 100
	}
	t.UpdatedAt = now()
	return t, nil
}

func (s *Store) LogTime(projectID, id shared.ID, minutes int) (*tasks.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.ProjectID != projectID {
		return nil, notFound("task", id)
	}
	// A real store would append to a time_entries table; here we just advance
	// progress proportionally so the endpoint has an observable effect.
	t.ProgressPct = min(100, t.ProgressPct+minutes/15)
	t.UpdatedAt = now()
	return t, nil
}

func (s *Store) AddComment(projectID, id shared.ID, author, body string) (*tasks.Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.ProjectID != projectID {
		return nil, notFound("task", id)
	}
	c := tasks.Comment{ID: newID(), TaskID: id, Author: shared.ID(author), Body: body, Timestamps: stamps()}
	s.comments[id] = append(s.comments[id], c)
	return &c, nil
}

func (s *Store) TaskExists(projectID, id shared.ID) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	return ok && t.ProjectID == projectID
}

// ---- attachments ----

func (s *Store) AddAttachment(projectID, taskID shared.ID, a attachments.Attachment) (*attachments.Attachment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[taskID]
	if !ok || t.ProjectID != projectID {
		return nil, notFound("task", taskID)
	}
	a.ID = newID()
	a.TaskID = taskID
	a.Timestamps = stamps()
	s.attachments[taskID] = append(s.attachments[taskID], a)
	return &a, nil
}

func (s *Store) ListAttachments(req *attachments.ListAttachmentsReq) (*shared.Page[attachments.Attachment], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[req.TaskID]
	if !ok || t.ProjectID != req.ProjectID {
		return nil, notFound("task", req.TaskID)
	}
	out := append([]attachments.Attachment{}, s.attachments[req.TaskID]...)
	return paginate(out, req.Limit), nil
}

// ---- admin ----

// IssueToken mints a new API token, stores its public record, and returns the
// one-time secret DTO (the raw secret is never persisted in cleartext by a real
// store - hash it before saving).
func (s *Store) IssueToken(name, scope string) *admin.ApiTokenSecret {
	secret := "tf_" + string(newID()) + string(newID())
	last4 := secret[len(secret)-4:]
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &admin.ApiToken{ID: newID(), Name: name, Scope: scope, Last4: last4, Timestamps: stamps()}
	s.tokens[t.ID] = t
	return &admin.ApiTokenSecret{ID: t.ID, Name: name, Secret: secret}
}

func (s *Store) ListTokens(limit *int) *shared.Page[admin.ApiToken] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []admin.ApiToken{}
	for _, t := range s.tokens {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return paginate(out, limit)
}

func (s *Store) RevokeToken(id shared.ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tokens[id]; !ok {
		return notFound("token", id)
	}
	delete(s.tokens, id)
	return nil
}

// Stats returns the admin dashboard counters.
func (s *Store) Stats() admin.AdminStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, list := range s.attachments {
		n += len(list)
	}
	return admin.AdminStats{Projects: len(s.projects), Tasks: len(s.tasks), Attachments: n}
}

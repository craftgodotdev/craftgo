// Package svccontext is the user-owned dependency container for the
// CraftGo showcase. The example uses an in-memory store keyed by id —
// real projects swap these maps for a database handle, a cache, an
// OTel tracer, etc.
package svccontext

import (
	"sync"
	"sync/atomic"
)

// ServiceContext aggregates everything a logic / middleware function
// might need at request time. The showcase keeps three stores —
// users, projects, tasks — to back the three services declared in
// the DSL.
type ServiceContext struct {
	mu sync.Mutex
	Middlewares

	Users    map[string]User
	Projects map[string]Project
	Tasks    map[string]Task

	userCounter    atomic.Int64
	projectCounter atomic.Int64
	taskCounter    atomic.Int64
	commentCounter atomic.Int64
}

// User is the in-memory shape stored under svc.Users. Only the
// fields the example logic touches are modelled here; the wire
// shape (in `internal/types/users/types.go`) carries the full
// validator-laden contract.
type User struct {
	ID    string
	Email string
	Name  string
	Bio   string
}

// Project is the in-memory shape stored under svc.Projects.
// Members are id-only here; logic populates the wire-level
// UserRef structures from the matching Users entry at response time.
type Project struct {
	ID          string
	Name        string
	Description string
	OwnerID     string
	MemberIDs   []string
}

// Task is the in-memory shape stored under svc.Tasks.
type Task struct {
	ID         string
	Title      string
	Status     string
	ProjectID  string
	AssigneeID string
	Comments   []Comment
}

// Comment is one entry in Task.Comments.
type Comment struct {
	ID        string
	AuthorID  string
	Body      string
	CreatedAt string
}

// NewServiceContext returns a fresh, empty ServiceContext.
func NewServiceContext() *ServiceContext {
	return &ServiceContext{
		Users:    map[string]User{},
		Projects: map[string]Project{},
		Tasks:    map[string]Task{},
	}
}

// Lock / Unlock expose the embedded mutex so logic files can keep
// per-request mutations atomic across the showcase's three stores.
func (s *ServiceContext) Lock()   { s.mu.Lock() }
func (s *ServiceContext) Unlock() { s.mu.Unlock() }

// NextUserID returns a fresh, monotonically-increasing user id.
func (s *ServiceContext) NextUserID() string { return "u" + itoa(int(s.userCounter.Add(1))) }

// NextProjectID returns a fresh project id.
func (s *ServiceContext) NextProjectID() string { return "p" + itoa(int(s.projectCounter.Add(1))) }

// NextTaskID returns a fresh task id.
func (s *ServiceContext) NextTaskID() string { return "t" + itoa(int(s.taskCounter.Add(1))) }

// NextCommentID returns a fresh comment id.
func (s *ServiceContext) NextCommentID() string { return "c" + itoa(int(s.commentCounter.Add(1))) }

// itoa is the inline integer→string helper so this file doesn't
// pull in `strconv` just for four counter formatters.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = '0' + byte(n%10)
		n /= 10
	}
	return string(buf[pos:])
}

package activity

import (
	"context"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"

	activityevents "github.com/craftgodotdev/craftgo/example/taskflow/internal/events/activity"
	tasks "github.com/craftgodotdev/craftgo/example/taskflow/internal/types/tasks"
)

// Group is the broker identity this deployable's activity consumers join.
// It is written here rather than in the design because a group is where a
// consumer resumes: on Kafka and JetStream the name IS the stored
// position, so it belongs to the deployment and not to the contract.
const Group craftevents.Group = "taskflow-activity"

// Handler implements the generated ActivityServiceHandler: it builds the
// feed from published contracts alone, never reading the task store.
//
// A payload reaches a method decoded and validated; returning an error
// tells the transport the message was not processed.
type Handler struct{ feed *Feed }

// NewHandler binds a handler set to the feed it appends to.
func NewHandler(feed *Feed) Handler { return Handler{feed: feed} }

func (h Handler) RecordTaskCreated(_ context.Context, payload *tasks.TaskCreated) error {
	h.feed.Append(Entry{
		ProjectID: string(payload.ProjectID),
		TaskID:    string(payload.TaskID),
		Summary:   "created " + payload.Title,
		At:        payload.CreatedAt,
	})
	return nil
}

func (h Handler) RecordStatusChange(_ context.Context, payload *tasks.TaskStatusChanged) error {
	h.feed.Append(Entry{
		ProjectID: string(payload.ProjectID),
		TaskID:    string(payload.TaskID),
		Summary:   "status " + string(payload.From) + " -> " + string(payload.To),
		At:        payload.ChangedAt,
	})
	return nil
}

// Register binds the handler set to bus under Group. It is the one line a
// deployable writes per service it runs; nothing is delivered until
// [craftevents.Bus.Start].
func Register(bus *craftevents.Bus, feed *Feed) error {
	return activityevents.RegisterActivityServiceHandler(bus, NewHandler(feed), nil,
		activityevents.ActivityServiceGroups{Default: Group})
}

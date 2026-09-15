package activity

import (
	"context"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"

	tasksevents "github.com/craftgodotdev/craftgo/example/taskflow/internal/events/tasks"
	tasks "github.com/craftgodotdev/craftgo/example/taskflow/internal/types/tasks"
)

// Group is the broker identity this deployable's activity subscriptions
// join. It is written here rather than in the design because a group is
// where a listener resumes: on Kafka and JetStream the name IS the stored
// position, so it belongs to the deployment and not to the contract.
const Group craftevents.Group = "taskflow-activity"

// Handler builds the feed from published contracts alone, never reading
// the task store.
//
// A payload reaches a method decoded and validated; returning an error
// tells the transport the message was not processed.
type Handler struct{ feed *Feed }

// NewHandler binds the logic to the feed it appends to.
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

// Register lists what this module listens to: one line per contract,
// naming the group it joins and the method it dispatches to. The design
// declares the contracts and nothing else, so this - and not a generated
// file - is where a reader finds the answer.
//
// Nothing is delivered until [craftevents.Bus.Start].
func Register(bus *craftevents.Bus, feed *Feed) error {
	h := NewHandler(feed)
	return bus.RegisterAll(
		tasksevents.TaskCreated.Subscription(bus, Group, h.RecordTaskCreated),
		tasksevents.TaskStatusChanged.Subscription(bus, Group, h.RecordStatusChange),
	)
}

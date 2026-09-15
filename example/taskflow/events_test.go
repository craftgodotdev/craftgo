package main

import (
	"context"
	"testing"

	craftevents "github.com/craftgodotdev/craftgo/pkg/events"
	"github.com/craftgodotdev/craftgo/pkg/events/codecjson"
	"github.com/craftgodotdev/craftgo/pkg/events/memory"

	"github.com/craftgodotdev/craftgo/example/taskflow/config"
	"github.com/craftgodotdev/craftgo/example/taskflow/internal/activity"
	taskservice "github.com/craftgodotdev/craftgo/example/taskflow/internal/service/task_service"
	project "github.com/craftgodotdev/craftgo/example/taskflow/internal/types/project"
	tasks "github.com/craftgodotdev/craftgo/example/taskflow/internal/types/tasks"
	"github.com/craftgodotdev/craftgo/example/taskflow/svccontext"
)

// bootEvents builds the wiring main.go builds: one bus over the
// in-process transport, the bus on the ServiceContext so logic can
// publish through the generated descriptors, and the activity module's
// subscriptions registered and started.
func bootEvents(t *testing.T) (*svccontext.ServiceContext, *memory.Transport) {
	t.Helper()
	transport := memory.New(memory.WithErrorHandler(func(sub craftevents.Subscription, _ *craftevents.Message, err error) {
		t.Errorf("consumer %s failed: %v", sub.Consumer, err)
	}))
	bus := craftevents.New(
		craftevents.WithTransport(transport),
		craftevents.WithCodec(codecjson.Codec{}),
	)
	svc := svccontext.NewServiceContext(&config.Config{})
	svc.Bus = bus
	if err := activity.Register(bus, svc.Activity); err != nil {
		t.Fatalf("register consumers: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		t.Fatalf("start consumers: %v", err)
	}
	return svc, transport
}

// Task logic publishes a contract; activity logic in another package
// consumes it. Neither imports the other - the only thing they share is
// the contract the design declares.
func TestTaskLifecycleReachesTheActivityFeed(t *testing.T) {
	svc, transport := bootEvents(t)
	ctx := context.Background()

	proj, err := svc.Store.CreateProject(&project.CreateProjectReq{Key: "apollo", Name: "Apollo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	created, err := taskservice.NewCreateTaskService(ctx, svc).CreateTask(&tasks.CreateTaskReq{
		ProjectID: proj.ID,
		Title:     "ship the event model",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	done := tasks.TaskStatusDone
	if _, err := taskservice.NewSetTaskStatusService(ctx, svc).SetTaskStatus(&tasks.SetTaskStatusReq{
		ProjectID: proj.ID,
		ID:        created.ID,
		Status:    done,
	}); err != nil {
		t.Fatalf("set status: %v", err)
	}
	transport.Drain()

	// Two different contracts deliver concurrently, so the feed holds
	// both entries in no guaranteed order.
	feed := svc.Activity.For(string(proj.ID))
	if len(feed) != 2 {
		t.Fatalf("activity feed = %d entries, want 2:\n%+v", len(feed), feed)
	}
	seen := map[string]bool{}
	for _, e := range feed {
		seen[e.Summary] = true
		if e.TaskID != string(created.ID) {
			t.Errorf("entry %+v is not about the created task", e)
		}
	}
	for _, want := range []string{"created ship the event model", "status todo -> done"} {
		if !seen[want] {
			t.Errorf("feed %+v is missing %q", feed, want)
		}
	}
}

// A status write that changes nothing publishes nothing: the design says
// the event reports a transition, so logic only announces a real one.
func TestUnchangedStatusPublishesNothing(t *testing.T) {
	svc, transport := bootEvents(t)
	ctx := context.Background()

	proj, err := svc.Store.CreateProject(&project.CreateProjectReq{Key: "gemini", Name: "Gemini"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	created, err := taskservice.NewCreateTaskService(ctx, svc).CreateTask(&tasks.CreateTaskReq{
		ProjectID: proj.ID,
		Title:     "hold",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := taskservice.NewSetTaskStatusService(ctx, svc).SetTaskStatus(&tasks.SetTaskStatusReq{
		ProjectID: proj.ID,
		ID:        created.ID,
		Status:    created.Status,
	}); err != nil {
		t.Fatalf("set status: %v", err)
	}
	transport.Drain()

	if feed := svc.Activity.For(string(proj.ID)); len(feed) != 1 {
		t.Fatalf("activity feed = %d entries, want only the creation:\n%+v", len(feed), feed)
	}
}

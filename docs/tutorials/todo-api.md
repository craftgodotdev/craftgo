# Tutorial: Build a TODO API

This tutorial builds a small but realistic CRUD service end to end: list, get, create, update, and delete todos — with enums, validation, pagination, and an OpenAPI spec. By the end you'll understand the full craftgo loop: **write DSL → generate → fill logic → run**.

It assumes you've skimmed [Getting Started](/guide/getting-started). Budget ~15 minutes.

## 1. Scaffold

```bash
mkdir todo && cd todo
go mod init example.com/todo
go get github.com/craftgodotdev/craftgo
craftgo init design
```

`craftgo init` writes `design/craftgo.design.yaml` with sensible defaults. Open it and confirm the base path:

```yaml
openapi:
  title: Todo API
  version: 0.1.0
  basePath: /api
```

## 2. Model the data

Create `design/todos/types.craftgo`. Start with two enums and the core type:

```craftgo
package todos

enum TodoStatus {
    Open       = "open"
    InProgress = "in_progress"
    Done       = "done"
}

enum TodoPriority {
    Low    = "low"
    Medium = "medium"
    High   = "high"
}

type Todo {
    id        string       @length(1, 64)
    title     string       @length(1, 200)
    notes     string?      @maxLength(2000)
    status    TodoStatus
    priority  TodoPriority @default(Medium)
    tags      string[]     @maxItems(10) @uniqueItems
    createdAt string       @format(datetime)
}
```

Things to notice:

- **Enums** are string-valued here (`= "open"`), so they marshal as those strings on the wire and craftgo generates a validity check.
- `notes string?` — the `?` makes it optional (a Go pointer, omitted from JSON when nil).
- `@default(Medium)` references an enum value by **bare name**, not a string.
- `tags string[]` with `@maxItems` + `@uniqueItems` validates the array.

## 3. Request shapes

Add the request/response types to the same file. Each endpoint gets its own request type — that keeps validation precise per operation.

```craftgo
type CreateTodoReq {
    title    string       @length(1, 200)
    notes    string?      @maxLength(2000)
    status   TodoStatus
    priority TodoPriority @default(Medium)
    tags     string[]?    @maxItems(10) @uniqueItems
}

type UpdateTodoReq {
    id       string  @path @length(1, 64)
    title    string? @length(1, 200)
    notes    string? @maxLength(2000)
    status   TodoStatus?
    priority TodoPriority?
}

type GetTodoReq {
    id string @path @length(1, 64)
}

type ListTodosReq {
    cursor string?     @query
    limit  int?        @query @gte(1) @lte(100)
    status TodoStatus? @query
}

type TodoList {
    items  Todo[]
    cursor string?
}

type OkResp {
    ok bool
}
```

`@path` binds a field to a URL path parameter; `@query` binds it to the query string. `UpdateTodoReq` is a PATCH shape — every field except `id` is optional, so callers send only what changes.

## 4. Define the service

Create `design/todos/service.craftgo`:

```craftgo
package todos

@prefix("/todos")
@tags(todos)
service TodoService {
    @doc("List todos with cursor pagination, optional status filter.")
    get ListTodos / {
        request  ListTodosReq
        response TodoList
    }

    @doc("Fetch one todo by id.")
    get GetTodo /{id} {
        request  GetTodoReq
        response Todo
    }

    @doc("Create a new todo.")
    post CreateTodo / {
        request  CreateTodoReq
        response Todo
    }

    @doc("Patch a todo. Only supplied fields are updated.")
    patch UpdateTodo /{id} {
        request  UpdateTodoReq
        response Todo
    }

    @doc("Delete a todo. Idempotent.")
    delete DeleteTodo /{id} {
        request  GetTodoReq
        response OkResp
    }
}
```

`@prefix("/todos")` prepends to every route; combined with `basePath: /api`, `GetTodo` lands at `GET /api/todos/{id}`. The `{id}` segment matches the `id @path` field in the request type — craftgo verifies that linkage at generate time.

## 5. Generate

```bash
craftgo gen design
```

Inspect what landed:

```
internal/
├── types/todos/        types.go, validate.go, enums.go, errors.go
├── transport/todo-service/   list-todos.go, get-todo.go, ... (handlers)
├── service/todo-service/     list-todos.go, ... (logic stubs)
└── routes/...
docs/openapi.yaml
main.go
```

Open `internal/types/todos/validate.go` — every decorator you wrote is now a plain `if`. Open `docs/openapi.yaml` — every endpoint, schema, and enum is there.

## 6. Fill the logic

Edit the stubs in `internal/service/todo-service/`. They are gen-once — `craftgo gen` will never overwrite them. A trivial in-memory store:

```go
// internal/service/todo-service/create-todo.go
func (l *CreateTodoService) CreateTodo(req *types.CreateTodoReq) (*types.Todo, error) {
    t := &types.Todo{
        ID:        newID(),
        Title:     req.Title,
        Notes:     req.Notes,
        Status:    req.Status,
        Priority:  req.Priority,   // already defaulted to Medium by the handler
        Tags:      req.Tags,
        CreatedAt: time.Now().UTC().Format(time.RFC3339),
    }
    store.Put(t)
    return t, nil
}
```

```go
// internal/service/todo-service/get-todo.go
func (l *GetTodoService) GetTodo(req *types.GetTodoReq) (*types.Todo, error) {
    t, ok := store.Get(req.ID)
    if !ok {
        return nil, errors.ErrNotFound   // a generated typed error → 404
    }
    return t, nil
}
```

By the time your function runs, the request is decoded, the path/query params are bound, and `req.Validate()` has passed. You only write the domain logic.

## 7. Run

```bash
go run .
# listening on :8080 (api)
```

```bash
# Create
curl -X POST localhost:8080/api/todos \
  -H 'Content-Type: application/json' \
  -d '{"title":"ship v1","status":"open","tags":["release"]}'

# Validation kicks in for free
curl -X POST localhost:8080/api/todos \
  -H 'Content-Type: application/json' \
  -d '{"title":"","status":"open"}'
# title: length out of range [1, 200]

# Bad enum value
curl -X POST localhost:8080/api/todos \
  -H 'Content-Type: application/json' \
  -d '{"title":"x","status":"frozen"}'
# status: invalid TodoStatus value
```

## 8. View the API docs

`docs/openapi.yaml` renders in any OpenAPI viewer:

```bash
npx @redocly/cli preview-docs docs/openapi.yaml
# or drop the file into editor.swagger.io
```

## What you learned

- **Types + enums + validators** in the DSL, validated at generate time and at runtime as plain Go.
- **One request type per operation**, with `@path` / `@query` binding and `@default` pre-fill.
- **A service block** maps verbs + paths to typed request/response pairs; `@prefix` + `basePath` compose the URL.
- **The regenerate loop**: transport/types/routes are regenerated; your logic in `internal/service/` is gen-once and safe.

## Next steps

- Add auth with [Middleware](/guide/middleware) and `@middlewares` / `@security`.
- Model failure with typed [Errors](/guide/errors) and `@errors(...)`.
- Split shared types into a `package shared` and reference them cross-package — see [Types & Scalars](/guide/types-and-scalars).
- Browse the full [Decorator Registry](/reference/decorator-registry).

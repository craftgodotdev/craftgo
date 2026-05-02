# Enums

An `enum` declares a closed set of values. craftgo emits a Go type, one constant per value, and the matching schema in OpenAPI.

## At a glance

```craftgo
enum Status {
    Active
    Inactive
    Pending
}
```

Three forms cover most use cases:

- **Bare** (`Active`) - wire payload equals the identifier, simplest case
- **Integer** (`Low = 1`) - ordered or weighted values
- **String** (`Red = "red"`) - custom wire payload

All values in one enum share the same form. Reference an enum value in `@default(...)` with the bare identifier; in your code, with the generated `<Enum><Value>` constant (`StatusActive`).

## Three forms

### Bare values

```craftgo
enum Status {
    Active
    Inactive
    Pending
}
```

Generated Go:

```go
type Status string

const (
    StatusActive   Status = "Active"
    StatusInactive Status = "Inactive"
    StatusPending  Status = "Pending"
)
```

The wire payload equals the identifier verbatim. Useful when you want the JSON value to be a stable, human-readable label.

### Integer values

```craftgo
enum Priority {
    Low    = 1
    Medium = 2
    High   = 3
}
```

```go
type Priority int

const (
    PriorityLow    Priority = 1
    PriorityMedium Priority = 2
    PriorityHigh   Priority = 3
)
```

Useful for ordered or weighted values where the integer carries meaning.

### String values

```craftgo
enum Color {
    Red   = "red"
    Green = "green"
    Blue  = "blue"
}
```

```go
type Color string

const (
    ColorRed   Color = "red"
    ColorGreen Color = "green"
    ColorBlue  Color = "blue"
)
```

Useful when the wire payload follows a different convention than Go identifiers (lowercase, hyphenated, etc.).

## Mixed kinds are not allowed

All values in an enum share one kind. The semantic analyzer rejects:

```craftgo
enum Bad {
    First  = 1
    Second = "two"  // mixed kind, error
}
```

## Using enums in fields

```craftgo
type Task {
    title    string
    status   Status
    priority Priority?
}
```

The Go field type is the enum's Go type:

```go
type Task struct {
    Title    string   `json:"title"`
    Status   Status   `json:"status"`
    Priority *Priority `json:"priority,omitempty"`
}
```

## Defaults reference the value name

Use the bare identifier in `@default(...)`:

```craftgo
type Task {
    status Status @default(Pending)
}
```

The handler pre-fills the field with the matching Go constant:

```go
req.Status = StatusPending
```

You cannot use the wire form (`@default("pending")`) - the canonical form is the identifier so the binding stays stable even if you change the wire payload later.

## Enums in arrays

```craftgo
type Workflow {
    allowed Status[] @default([Active, Pending])
}
```

```go
req.Allowed = []Status{StatusActive, StatusPending}
```

## OpenAPI emission

Each enum becomes a schema entry with the values listed:

```yaml
components:
  schemas:
    Status:
      type: string
      enum: [Active, Inactive, Pending]
    Priority:
      type: integer
      enum: [1, 2, 3]
```

Field references use `$ref` to the schema:

```yaml
properties:
  status:
    $ref: '#/components/schemas/Status'
```

## Doc comments

```craftgo
// Status describes the task's lifecycle phase.
//
//   - Active   = currently being worked on
//   - Inactive = paused or archived
//   - Pending  = not yet started
enum Status { Active  Inactive  Pending }
```

The doc surfaces in:

- Hover popups in the LSP
- The generated Go file (above the `type Status` declaration)
- The OpenAPI schema's `description` field

Per-value docs work too:

```craftgo
enum Status {
    Active   @doc("Currently being worked on.")
    Inactive @doc("Paused or archived.")
    Pending  @doc("Not yet started.")
}
```

## Marshaling

Bare and string-valued enums marshal as JSON strings. Integer enums marshal as numbers. craftgo does not generate a custom `MarshalJSON`; the underlying Go type's natural JSON behavior wins.

If you need custom marshaling (e.g., always emit lowercase regardless of the constant name), add a `MarshalJSON` method in a non-generated file. Generated code will not overwrite it.

## Restrictions

- Enum values cannot share a Go-side identifier (`Active` and `active` would both produce `StatusActive`); the analyzer flags collisions
- Enum names follow Go's exported convention - lower-case names produce unexported types and are flagged with a warning
- Empty enums are not allowed

## When to prefer scalars

Scalars and enums look similar from a distance but solve different problems:

| Use enum   | Use scalar |
| ---------- | ---------- |
| Closed set of named values | Format-restricted primitive |
| Fixed at design time | Free-form value matching a pattern |
| Wire payload is a label  | Wire payload is the value itself |

`Color { Red, Green, Blue }` is an enum. `HexColor` (any string matching `^#[0-9A-Fa-f]{6}$`) is a scalar.

package semantic

import "testing"

// ---------- @sensitive: standalone is fine ----------

func TestSensitiveAlone(t *testing.T) {
	mustClean(t, `package design
type User {
	id        string
	internal  string @sensitive
}`)
}

func TestSensitiveWithDocAndDeprecatedAllowed(t *testing.T) {
	// Metadata decorators don't shape wire behaviour so they coexist
	// fine with @sensitive.
	mustClean(t, `package design
type User {
	id        string
	internal  string @sensitive @doc("server-only") @deprecated
}`)
}

// ---------- @sensitive + validators ----------

func TestSensitiveConflictsRequired(t *testing.T) {
	d := expectError(t, `package design
type User { secret string @sensitive @required }`, CodeDecoratorConflict)
	expectMessage(t, d, "@required cannot be combined with @sensitive")
}

func TestSensitiveConflictsLength(t *testing.T) {
	d := expectError(t, `package design
type User { secret string @sensitive @length(1, 80) }`, CodeDecoratorConflict)
	expectMessage(t, d, "@length cannot be combined with @sensitive")
}

func TestSensitiveConflictsPattern(t *testing.T) {
	d := expectError(t, `package design
type User { secret string @sensitive @pattern("^x") }`, CodeDecoratorConflict)
	expectMessage(t, d, "@pattern cannot be combined with @sensitive")
}

func TestSensitiveConflictsFormat(t *testing.T) {
	d := expectError(t, `package design
type User { secret string @sensitive @format(email) }`, CodeDecoratorConflict)
	expectMessage(t, d, "@format cannot be combined with @sensitive")
}

func TestSensitiveConflictsMinMax(t *testing.T) {
	d := expectError(t, `package design
type User { age int @sensitive @min(0) }`, CodeDecoratorConflict)
	expectMessage(t, d, "@min cannot be combined with @sensitive")
}

// ---------- @sensitive + nullability / default ----------

func TestSensitiveConflictsNullable(t *testing.T) {
	d := expectError(t, `package design
type User { secret string @sensitive @nullable }`, CodeDecoratorConflict)
	expectMessage(t, d, "@nullable cannot be combined with @sensitive")
}

func TestSensitiveConflictsDefault(t *testing.T) {
	d := expectError(t, `package design
type User { tier string @sensitive @default("free") }`, CodeDecoratorConflict)
	expectMessage(t, d, "@default cannot be combined with @sensitive")
}

// ---------- @sensitive + binding decorators ----------

func TestSensitiveConflictsBody(t *testing.T) {
	d := expectError(t, `package design
type Req { secret string @sensitive @body }`, CodeDecoratorConflict)
	expectMessage(t, d, "@body cannot be combined with @sensitive")
}

func TestSensitiveConflictsPath(t *testing.T) {
	d := expectError(t, `package design
type Req { id string @sensitive @path }`, CodeDecoratorConflict)
	expectMessage(t, d, "@path cannot be combined with @sensitive")
}

func TestSensitiveConflictsQuery(t *testing.T) {
	d := expectError(t, `package design
type Req { token string @sensitive @query }`, CodeDecoratorConflict)
	expectMessage(t, d, "@query cannot be combined with @sensitive")
}

func TestSensitiveConflictsHeader(t *testing.T) {
	d := expectError(t, `package design
type Req { token string @sensitive @header }`, CodeDecoratorConflict)
	expectMessage(t, d, "@header cannot be combined with @sensitive")
}

func TestSensitiveConflictsCookie(t *testing.T) {
	d := expectError(t, `package design
type Req { sid string @sensitive @cookie }`, CodeDecoratorConflict)
	expectMessage(t, d, "@cookie cannot be combined with @sensitive")
}

func TestSensitiveConflictsForm(t *testing.T) {
	d := expectError(t, `package design
type Req { secret string @sensitive @form }`, CodeDecoratorConflict)
	expectMessage(t, d, "@form cannot be combined with @sensitive")
}

// ---------- @sensitive on error fields ----------

func TestSensitiveOnErrorFieldAlone(t *testing.T) {
	mustClean(t, `package design
error ServiceUnavailable Maintenance {
	msg      string
	internal string @sensitive
}`)
}

func TestSensitiveConflictsOnErrorField(t *testing.T) {
	d := expectError(t, `package design
error BadRequest Bad {
	internal string @sensitive @required
}`, CodeDecoratorConflict)
	expectMessage(t, d, "@required cannot be combined with @sensitive")
}

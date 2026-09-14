// Package compat compares two AsyncAPI documents and reports the changes
// that would break somebody already using the contracts.
//
// The document is the artifact craftgo publishes, so it is what an
// evolution check has to defend. Every rule here is decidable from the two
// documents alone - no runtime data, no registry - which is what lets a CI
// job answer "is this release safe for existing consumers?".
package compat

import (
	"fmt"
	"sort"
	"strings"
)

// Change is one difference between two versions of a contract set.
type Change struct {
	// Contract is the wire name the change affects, "" for a change that
	// is not tied to one contract.
	Contract string
	// Path locates the change inside the payload, e.g. `orderId` or
	// `items[].sku`. Empty when the contract itself changed.
	Path string
	// Breaking reports whether an existing consumer or publisher stops
	// working. Non-breaking changes are reported too, so a release note
	// can list them.
	Breaking bool
	// Detail is the human-readable description.
	Detail string
}

// String renders the change the way the CLI prints it.
func (c Change) String() string {
	where := c.Contract
	if c.Path != "" {
		where += "." + c.Path
	}
	kind := "changed"
	if c.Breaking {
		kind = "BREAKING"
	}
	if where == "" {
		return fmt.Sprintf("%s: %s", kind, c.Detail)
	}
	return fmt.Sprintf("%s %s: %s", kind, where, c.Detail)
}

// Doc is the slice of an AsyncAPI document this check reads.
type Doc struct {
	// Contracts maps a channel address (the wire contract name) to the
	// name of the schema its payload resolves to.
	Contracts map[string]string
	// Schemas is `components.schemas`, keyed by component name.
	Schemas map[string]any
	// Sends holds the contracts this document's application publishes -
	// the channels it declares a `send` operation for. A contract losing
	// its producer is invisible in the schemas, so it is tracked here.
	Sends map[string]bool
	// Receives holds one entry per `receive` operation, keyed by the
	// operation name, which is derived from the service and consumer and
	// so survives a group rename.
	Receives map[string]Receive
}

// Receive is one consumer's read of one contract.
type Receive struct {
	// Contract is the channel address the operation reads.
	Contract string
	// Group is the broker identity the consumer joins under - where its
	// position in the stream lives.
	Group string
}

// Compare reports how next differs from prev, breaking changes first and
// deterministically ordered.
func Compare(prev, next Doc) []Change {
	var out []Change
	for _, contract := range sortedKeys(prev.Contracts) {
		nextSchema, ok := next.Contracts[contract]
		if !ok {
			out = append(out, Change{Contract: contract, Breaking: true,
				Detail: "contract removed - consumers subscribed to it stop receiving messages"})
			continue
		}
		out = append(out, comparePayload(contract, prev, next, prev.Contracts[contract], nextSchema)...)
		if prev.Sends[contract] && !next.Sends[contract] {
			out = append(out, Change{Contract: contract, Breaking: true,
				Detail: "this application no longer publishes the contract - consumers keep waiting on a channel nobody writes to"})
		}
		if !prev.Sends[contract] && next.Sends[contract] {
			out = append(out, Change{Contract: contract,
				Detail: "this application now publishes the contract"})
		}
	}
	for _, contract := range sortedKeys(next.Contracts) {
		if _, ok := prev.Contracts[contract]; !ok {
			out = append(out, Change{Contract: contract, Detail: "contract added"})
		}
	}
	out = append(out, compareGroups(prev, next)...)
	out = dropSubsumed(out)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Breaking && !out[j].Breaking })
	return out
}

// compareGroups reports a consumer whose group name changed. The group is
// where the broker keeps that consumer's position, so a new name is a
// consumer with no position at all.
func compareGroups(prev, next Doc) []Change {
	var out []Change
	for _, op := range sortedKeys(prev.Receives) {
		was := prev.Receives[op]
		now, ok := next.Receives[op]
		if !ok || was.Group == "" || now.Group == "" || was.Group == now.Group {
			continue
		}
		out = append(out, Change{Contract: was.Contract, Breaking: true,
			Detail: fmt.Sprintf("consumer group renamed from %q to %q - the broker holds no position under the new name, so consumers resume from no committed position and replay or skip the stream", was.Group, now.Group)})
	}
	return out
}

// dropSubsumed removes a non-breaking note about a location a breaking
// change already reports - a field that is both new and required says
// everything in the breaking line.
func dropSubsumed(changes []Change) []Change {
	broken := map[string]bool{}
	for _, c := range changes {
		if c.Breaking {
			broken[c.Contract+"|"+c.Path] = true
		}
	}
	out := changes[:0]
	for _, c := range changes {
		if !c.Breaking && c.Path != "" && broken[c.Contract+"|"+c.Path] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// comparePayload walks the two payload schemas of one contract.
func comparePayload(contract string, prev, next Doc, prevName, nextName string) []Change {
	return compareSchema(contract, "", prev, next, prev.Schemas[prevName], next.Schemas[nextName], map[string]bool{})
}

// compareSchema diffs one schema node, following $ref through the two
// documents' component maps. seen guards against a recursive type.
func compareSchema(contract, path string, prev, next Doc, a, b any, seen map[string]bool) []Change {
	am, bm := asMap(a), asMap(b)
	if am == nil || bm == nil {
		return nil
	}
	if ref := refName(am); ref != "" {
		key := path + "|" + ref
		if seen[key] {
			return nil
		}
		seen[key] = true
		return compareSchema(contract, path, prev, next, prev.Schemas[ref], next.Schemas[refName(bm)], seen)
	}
	var out []Change
	out = append(out, compareType(contract, path, am, bm)...)
	out = append(out, compareEnum(contract, path, am, bm)...)
	out = append(out, compareRequired(contract, path, am, bm)...)
	out = append(out, compareProperties(contract, path, prev, next, am, bm, seen)...)
	out = append(out, compareSchema(contract, join(path, "[]"), prev, next, am["items"], bm["items"], seen)...)
	return out
}

// compareType reports a changed JSON type, which breaks any decoder.
func compareType(contract, path string, a, b map[string]any) []Change {
	at, bt := str(a["type"]), str(b["type"])
	if at == "" || bt == "" || at == bt {
		return nil
	}
	return []Change{{Contract: contract, Path: path, Breaking: true,
		Detail: fmt.Sprintf("type changed from %s to %s", at, bt)}}
}

// compareEnum reports removed enum members; adding one is safe for a
// publisher but not for a consumer that switches exhaustively, so it is
// reported as non-breaking with a note.
func compareEnum(contract, path string, a, b map[string]any) []Change {
	prev, next := enumSet(a["enum"]), enumSet(b["enum"])
	if len(prev) == 0 && len(next) == 0 {
		return nil
	}
	var out []Change
	for _, v := range sortedKeys(prev) {
		if !next[v] {
			out = append(out, Change{Contract: contract, Path: path, Breaking: true,
				Detail: fmt.Sprintf("enum value %q removed - a publisher may still send it", v)})
		}
	}
	for _, v := range sortedKeys(next) {
		if !prev[v] {
			out = append(out, Change{Contract: contract, Path: path,
				Detail: fmt.Sprintf("enum value %q added - consumers that switch exhaustively need updating", v)})
		}
	}
	return out
}

// compareRequired reports a property that became mandatory. Dropping a
// requirement is a widening and is safe.
func compareRequired(contract, path string, a, b map[string]any) []Change {
	prev, next := stringSet(a["required"]), stringSet(b["required"])
	var out []Change
	for _, name := range sortedKeys(next) {
		if !prev[name] {
			out = append(out, Change{Contract: contract, Path: join(path, name), Breaking: true,
				Detail: "became required - messages from publishers on the previous version are rejected"})
		}
	}
	for _, name := range sortedKeys(prev) {
		if !next[name] {
			out = append(out, Change{Contract: contract, Path: join(path, name),
				Detail: "no longer required"})
		}
	}
	return out
}

// compareProperties walks each property present in either version.
func compareProperties(contract, path string, prev, next Doc, a, b map[string]any, seen map[string]bool) []Change {
	ap, bp := asMap(a["properties"]), asMap(b["properties"])
	var out []Change
	for _, name := range sortedKeys(ap) {
		sub := join(path, name)
		if _, ok := bp[name]; !ok {
			out = append(out, Change{Contract: contract, Path: sub, Breaking: true,
				Detail: "field removed - consumers reading it lose the value"})
			continue
		}
		out = append(out, compareSchema(contract, sub, prev, next, ap[name], bp[name], seen)...)
	}
	for _, name := range sortedKeys(bp) {
		if _, ok := ap[name]; !ok {
			out = append(out, Change{Contract: contract, Path: join(path, name),
				Detail: "field added"})
		}
	}
	return out
}

// Breaking reports whether any change in the list breaks an existing user.
func Breaking(changes []Change) bool {
	for _, c := range changes {
		if c.Breaking {
			return true
		}
	}
	return false
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	if name == "[]" {
		return path + "[]"
	}
	return path + "." + name
}

func refName(m map[string]any) string {
	const prefix = "#/components/schemas/"
	if r := str(m["$ref"]); strings.HasPrefix(r, prefix) {
		return strings.TrimPrefix(r, prefix)
	}
	return ""
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func stringSet(v any) map[string]bool {
	out := map[string]bool{}
	list, _ := v.([]any)
	for _, item := range list {
		if s := str(item); s != "" {
			out[s] = true
		}
	}
	return out
}

func enumSet(v any) map[string]bool {
	out := map[string]bool{}
	list, _ := v.([]any)
	for _, item := range list {
		out[fmt.Sprint(item)] = true
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

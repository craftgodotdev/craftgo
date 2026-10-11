package docs

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"go.yaml.in/yaml/v2"
	yamlv3 "gopkg.in/yaml.v3"
)

// marshalDocument renders doc as YAML that YAML 1.1 and YAML 1.2 readers both
// read as doc: each number a number, each string a string.
func marshalDocument(doc *openapi3.T) ([]byte, error) {
	j, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	// A MapSlice keeps each object's keys in the JSON's order, which encoding/json sorts; yaml.v2
	// sorting a map's keys itself compares their digits as numbers, an order that is not total
	// (Op7RespBody, Op12ReqBody, Op71ReqBody), so the output varied with map iteration.
	var obj yaml.MapSlice
	if err := yaml.Unmarshal(escapeUnreadable(j), &obj); err != nil {
		return nil, err
	}
	out, err := yaml.Marshal(obj)
	if err != nil {
		return nil, err
	}
	return respellYAML11(out)
}

// escapeUnreadable spells each character of j, a JSON text, that a YAML
// reader refuses raw or YAML 1.1 takes for a line break - DEL, a C1 control,
// U+FFFE or U+FFFF - as a JSON escape: the YAML written from j double-quotes
// the string holding it and keeps the character escaped.
func escapeUnreadable(j []byte) []byte {
	if !bytes.ContainsFunc(j, unreadable) {
		return j
	}
	var b bytes.Buffer
	b.Grow(len(j) + 16)
	for _, r := range string(j) {
		if unreadable(r) {
			fmt.Fprintf(&b, `\u%04x`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.Bytes()
}

// unreadable reports whether a YAML reader refuses r raw or YAML 1.1 takes it
// for a line break.
func unreadable(r rune) bool {
	return r >= 0x7f && r <= 0x9f || r == 0xfffe || r == 0xffff
}

// yaml11Typed matches a plain scalar YAML 1.1 resolves to another type than a
// string: a bool, a null, an int or a float in any base, a timestamp, the
// merge key `<<` or the value key `=`, as PyYAML, the reader of Python's
// OpenAPI validators, resolves them.
var yaml11Typed = regexp.MustCompile(`^(?:` + strings.Join([]string{
	`y|Y|yes|Yes|YES|n|N|no|No|NO|true|True|TRUE|false|False|FALSE|on|On|ON|off|Off|OFF`,
	`~|null|Null|NULL|`,
	`[-+]?0b[0-1_]+|[-+]?0[0-7_]+|[-+]?(?:0|[1-9][0-9_]*)|[-+]?0x[0-9a-fA-F_]+|[-+]?[1-9][0-9_]*(?::[0-5]?[0-9])+`,
	`[-+]?[0-9][0-9_]*\.[0-9_]*(?:[eE][-+][0-9]+)?|\.[0-9][0-9_]*(?:[eE][-+][0-9]+)?`,
	`[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+\.[0-9_]*|[-+]?\.(?:inf|Inf|INF)|\.(?:nan|NaN|NAN)`,
	`[0-9]{4}-[0-9]{2}-[0-9]{2}`,
	`[0-9]{4}-[0-9]{1,2}-[0-9]{1,2}(?:[Tt]|[ \t]+)[0-9]{1,2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]*)?(?:[ \t]*(?:Z|[-+][0-9]{1,2}(?::[0-9]{2})?))?`,
	`<<|=`,
}, "|") + `)$`)

// respellYAML11 respells each scalar of out, block-style YAML, that YAML 1.1
// reads as another value than YAML 1.2: a float with an exponent and no dot
// (`1e-07`), a string to YAML 1.1, gets one (`1.0e-07`), and a plain string
// YAML 1.1 takes for another type (`=`, `<<`, `2001-12-14 21:59:43 -5`) gets
// double quotes.
func respellYAML11(out []byte) ([]byte, error) {
	var root yamlv3.Node
	if err := yamlv3.Unmarshal(out, &root); err != nil {
		return nil, err
	}
	var edits []scalarEdit
	var walk func(n *yamlv3.Node)
	walk = func(n *yamlv3.Node) {
		for _, c := range n.Content {
			walk(c)
		}
		if n.Kind != yamlv3.ScalarNode || n.Style != 0 {
			return
		}
		switch n.Tag {
		case "!!float":
			if mantissa, exponent, ok := strings.Cut(n.Value, "e"); ok && !strings.Contains(mantissa, ".") {
				edits = append(edits, scalarEdit{n.Line, n.Column, n.Value, mantissa + ".0e" + exponent})
			}
		case "!!int", "!!bool", "!!null":
		default:
			// The JSON the YAML is written from holds no other scalar than a
			// string.
			if n.Tag != "!!str" || yaml11Typed.MatchString(n.Value) {
				edits = append(edits, scalarEdit{n.Line, n.Column, n.Value, strconv.Quote(n.Value)})
			}
		}
	}
	walk(&root)
	return applyEdits(out, edits)
}

// scalarEdit respells as `as` the plain scalar `was`, which starts at rune
// column col of line, both counted from 1.
type scalarEdit struct {
	line, col int
	was, as   string
}

// applyEdits applies edits to out. A plain scalar ends its line, or the key
// its `:` follows; a long one written as a value may go on over the next
// lines, each of its line breaks standing for one space.
func applyEdits(out []byte, edits []scalarEdit) ([]byte, error) {
	if len(edits) == 0 {
		return out, nil
	}
	lines := strings.Split(string(out), "\n")
	// The last first, so an edit joining lines moves none an edit before it
	// names.
	slices.SortFunc(edits, func(a, b scalarEdit) int {
		return cmp.Or(cmp.Compare(b.line, a.line), cmp.Compare(b.col, a.col))
	})
	for _, e := range edits {
		runes := []rune(lines[e.line-1])
		if e.col < 1 || e.col > len(runes) {
			return nil, fmt.Errorf("respell YAML scalar %q: line %d has no column %d", e.was, e.line, e.col)
		}
		head, rest := string(runes[:e.col-1]), string(runes[e.col-1:])
		switch {
		case rest == e.was:
			lines[e.line-1] = head + e.as
		case strings.HasPrefix(rest, e.was+":"):
			lines[e.line-1] = head + e.as + strings.TrimPrefix(rest, e.was)
		default:
			text, end := rest, e.line
			for end < len(lines) && len(text) < len(e.was) {
				text += " " + strings.TrimSpace(lines[end])
				end++
			}
			if text != e.was {
				return nil, fmt.Errorf("respell YAML scalar %q: line %d holds %q", e.was, e.line, rest)
			}
			lines[e.line-1] = head + e.as
			lines = slices.Delete(lines, e.line, end)
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}

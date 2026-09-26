package docs

import (
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	yamlv3 "gopkg.in/yaml.v3"
	"sigs.k8s.io/yaml"
)

// marshalDocument renders doc as YAML whose numbers YAML 1.1 and YAML 1.2
// readers both read as numbers.
func marshalDocument(doc *openapi3.T) ([]byte, error) {
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return dotExponentFloats(out)
}

// dotExponentFloats spells each float out writes as an exponent without a dot,
// `1e-07`, which a YAML 1.1 reader takes for a string, as `1.0e-07`. In the
// block style of out, a scalar value ends its line.
func dotExponentFloats(out []byte) ([]byte, error) {
	var root yamlv3.Node
	if err := yamlv3.Unmarshal(out, &root); err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")
	var walk func(n *yamlv3.Node)
	walk = func(n *yamlv3.Node) {
		for _, c := range n.Content {
			walk(c)
		}
		mantissa, exponent, isExp := strings.Cut(n.Value, "e")
		if n.Kind != yamlv3.ScalarNode || n.Tag != "!!float" || !isExp || strings.Contains(mantissa, ".") {
			return
		}
		if line := lines[n.Line-1]; strings.HasSuffix(line, n.Value) {
			lines[n.Line-1] = strings.TrimSuffix(line, n.Value) + mantissa + ".0e" + exponent
		}
	}
	walk(&root)
	return []byte(strings.Join(lines, "\n")), nil
}

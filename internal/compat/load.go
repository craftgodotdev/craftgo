package compat

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads an AsyncAPI document and extracts the parts the check reads:
// each channel's contract name, the schema its payload resolves to, and
// each operation's action and consumer group.
func Load(path string) (Doc, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Doc{}, err
	}
	return Parse(body, path)
}

// Parse is [Load] over bytes already in hand.
func Parse(body []byte, name string) (Doc, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(body, &raw); err != nil {
		return Doc{}, fmt.Errorf("parse %s: %w", name, err)
	}
	components := asMap(raw["components"])
	doc := Doc{
		Contracts: map[string]string{},
		Schemas:   map[string]any{},
		Sends:     map[string]bool{},
		Receives:  map[string]Receive{},
	}
	for k, v := range asMap(components["schemas"]) {
		doc.Schemas[k] = v
	}
	messages := asMap(components["messages"])
	for _, ch := range asMap(raw["channels"]) {
		chm := asMap(ch)
		address := str(chm["address"])
		if address == "" {
			continue
		}
		// A channel names its message by $ref into components.messages;
		// the message's payload then names the schema.
		for _, m := range asMap(chm["messages"]) {
			id := messageID(str(asMap(m)["$ref"]))
			payload := asMap(asMap(messages[id])["payload"])
			if ref := refName(payload); ref != "" {
				doc.Contracts[address] = ref
			}
		}
	}
	// An operation names the channel it acts on: `send` for one this
	// application publishes, `receive` for one a consumer reads.
	for name, op := range asMap(raw["operations"]) {
		opm := asMap(op)
		ch := channelName(str(asMap(opm["channel"])["$ref"]))
		if ch == "" {
			continue
		}
		switch str(opm["action"]) {
		case "send":
			doc.Sends[ch] = true
		case "receive":
			doc.Receives[name] = Receive{Contract: ch, Group: str(opm["x-craftgo-group"])}
		}
	}
	return doc, nil
}

// channelName pulls the address out of a `#/channels/X` ref.
func channelName(ref string) string {
	const prefix = "#/channels/"
	if len(ref) > len(prefix) && ref[:len(prefix)] == prefix {
		return ref[len(prefix):]
	}
	return ""
}

// messageID pulls the component key out of a `#/components/messages/X` ref.
func messageID(ref string) string {
	const prefix = "#/components/messages/"
	if len(ref) > len(prefix) && ref[:len(prefix)] == prefix {
		return ref[len(prefix):]
	}
	return ""
}

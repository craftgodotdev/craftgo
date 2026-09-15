package events

import (
	"encoding/json"
	"sort"
)

// Plan is what a bus consumes: every registered group, and the consumers
// under it. It is the shape of a deployable's consumption, which no
// generated file states any more - the groups are the application's - so
// a project that wants that stated pins this in a golden file.
type Plan struct {
	Groups []PlanGroup `json:"groups"`
}

// PlanGroup is one broker identity and what it consumes.
type PlanGroup struct {
	Name      Group          `json:"name"`
	Consumers []PlanConsumer `json:"consumers"`
}

// PlanConsumer is one handler in a group.
type PlanConsumer struct {
	Event    string `json:"event"`
	Consumer string `json:"consumer"`
}

// Plan reports what is registered, before or after [Bus.Start]. Groups
// are ordered by name and consumers within a group by contract then
// consumer, so two runs of the same wiring produce the same plan.
func (b *Bus) Plan() Plan {
	b.mu.Lock()
	subs := make([]Subscription, len(b.subs))
	copy(subs, b.subs)
	b.mu.Unlock()

	byName := map[Group][]PlanConsumer{}
	for _, sub := range subs {
		byName[sub.Group] = append(byName[sub.Group], PlanConsumer{Event: sub.Event, Consumer: sub.Consumer})
	}
	out := Plan{Groups: make([]PlanGroup, 0, len(byName))}
	for name, consumers := range byName {
		out.Groups = append(out.Groups, PlanGroup{Name: name, Consumers: consumers})
	}
	return orderedPlan(out)
}

// MarshalJSON renders the plan in its own order rather than the one it
// was built in, so a golden file compares a plan and not a map iteration.
func (p Plan) MarshalJSON() ([]byte, error) {
	type plain Plan
	return json.Marshal(plain(orderedPlan(p)))
}

// orderedPlan is p sorted, with every slice non-nil so an empty plan
// renders as `{"groups":[]}` rather than as a null.
func orderedPlan(p Plan) Plan {
	out := Plan{Groups: make([]PlanGroup, 0, len(p.Groups))}
	for _, g := range p.Groups {
		consumers := make([]PlanConsumer, len(g.Consumers))
		copy(consumers, g.Consumers)
		sort.Slice(consumers, func(i, j int) bool {
			if consumers[i].Event != consumers[j].Event {
				return consumers[i].Event < consumers[j].Event
			}
			return consumers[i].Consumer < consumers[j].Consumer
		})
		out.Groups = append(out.Groups, PlanGroup{Name: g.Name, Consumers: consumers})
	}
	sort.Slice(out.Groups, func(i, j int) bool { return out.Groups[i].Name < out.Groups[j].Name })
	return out
}

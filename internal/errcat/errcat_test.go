package errcat

import "testing"

func TestCatalogue(t *testing.T) {
	if len(Categories) == 0 {
		t.Fatal("Categories is empty")
	}
	seen := map[string]bool{}
	prevStatus := 0
	for _, c := range Categories {
		if seen[c.Name] {
			t.Errorf("duplicate category %q", c.Name)
		}
		seen[c.Name] = true
		if c.Status < prevStatus {
			t.Errorf("Categories must be in ascending status order; %q (%d) follows %d", c.Name, c.Status, prevStatus)
		}
		prevStatus = c.Status
		if c.Status < 400 || c.Status > 599 {
			t.Errorf("%q has non-error status %d", c.Name, c.Status)
		}
		if c.Message == "" {
			t.Errorf("%q has no default message", c.Name)
		}
		if !IsCategory(c.Name) || Status(c.Name) != c.Status || Message(c.Name) != c.Message {
			t.Errorf("lookups disagree with the table for %q", c.Name)
		}
	}
	if IsCategory("Bogus") || Status("Bogus") != 0 || Message("Bogus") != "" {
		t.Error("unknown category should report not-a-category / zero status / empty message")
	}
}

package config

import (
	"reflect"
	"strings"
	"testing"
)

// notAPath names the Output fields that select a mode rather than name a
// directory. Every other string field is generated into and so is imported
// as `<module>/<path>`, which cannot name a directory outside the module.
// A new field must be classified here or the test below fails, which is
// what stops the next path key shipping unguarded.
var notAPath = map[string]bool{"Kind": true, "FileCase": true}

func TestEveryOutputPathStaysInsideTheProject(t *testing.T) {
	typ := reflect.TypeOf(Output{})
	checked := 0
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.String || notAPath[f.Name] {
			continue
		}
		checked++
		t.Run(f.Name, func(t *testing.T) {
			cfg := &Config{}
			reflect.ValueOf(&cfg.Output).Elem().Field(i).SetString("../outside")
			err := cfg.validate()
			if err == nil {
				t.Fatalf("output.%s accepted a path outside the project - gen writes there and the import fails later at `go build`", f.Name)
			}
			if !strings.Contains(err.Error(), "must stay inside the project") {
				t.Errorf("output.%s rejected with the wrong reason: %v", f.Name, err)
			}
		})
	}
	if checked == 0 {
		t.Fatal("no path fields found - the reflection walk is broken, not the guard")
	}
}

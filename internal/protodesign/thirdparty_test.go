package protodesign

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	// The runtime a generated service links in, so its protos register here.
	_ "github.com/craftgodotdev/craftgo/pkg/rpc"
	_ "github.com/craftgodotdev/craftgo/pkg/server"
	_ "github.com/craftgodotdev/craftgo/pkg/telemetry"
)

// Every proto file the runtime registers lies where runtimeProtos refuses a design copy.
func TestRuntimeProtosCoverWhatTheRuntimeRegisters(t *testing.T) {
	var missing []string
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !linkedByRuntime(fd.Path()) {
			missing = append(missing, fd.Path())
		}
		return true
	})
	if len(missing) > 0 {
		t.Errorf("registered by the runtime but missing from runtimeProtos: %v", missing)
	}
	for name, want := range map[string]bool{
		"google/rpc/status.proto":   true,
		"google/api/httpbody.proto": true,
		"google/api/http.proto":     false,
		"grpc/health/v1/x.proto":    true,
		"grpc/greeter.proto":        false,
		"google/protobuf":           false,
	} {
		if got := linkedByRuntime(name); got != want {
			t.Errorf("linkedByRuntime(%q) = %v", name, got)
		}
	}
}

// Only an import path of another fetchable module is foreign: a relative or dotless path, or one
// inside the module, is not.
func TestForeignPackage(t *testing.T) {
	for p, want := range map[string]bool{
		"github.com/elsewhere/a":     true,
		"example.com/app":            false,
		"example.com/app/gen/a":      false,
		"example.com/application/x":  true,
		"./a":                        false,
		"../a":                       false,
		"apb":                        false,
		"proto/greet":                false,
		"":                           false,
		"google.golang.org/x/status": true,
	} {
		if got := foreignPackage(p, module); got != want {
			t.Errorf("foreignPackage(%q) = %v", p, got)
		}
	}
}

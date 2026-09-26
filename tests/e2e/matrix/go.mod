module github.com/craftgodotdev/craftgo/tests/e2e/matrix

go 1.26.6

require (
	github.com/craftgodotdev/craftgo v0.0.0
	github.com/craftgodotdev/craftgo/pkg/events/kafka v0.0.0
	github.com/twmb/franz-go v1.22.0
	github.com/twmb/franz-go/pkg/kfake v0.0.0-20260914031441-1623827d1042
	github.com/twmb/franz-go/pkg/kmsg v1.14.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260825221802-da73d73af1c5
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/pierrec/lz4/v4 v4.1.30 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/craftgodotdev/craftgo/pkg/events v1.10.0
	github.com/craftgodotdev/craftgo/pkg/wire v0.0.0
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
)

replace github.com/craftgodotdev/craftgo => ../../..

replace github.com/craftgodotdev/craftgo/pkg/events => ../../../pkg/events

replace github.com/craftgodotdev/craftgo/pkg/events/kafka => ../../../pkg/events/kafka

replace github.com/craftgodotdev/craftgo/pkg/wire => ../../../pkg/wire

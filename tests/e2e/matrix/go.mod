module github.com/craftgodotdev/craftgo/tests/e2e/matrix

go 1.26.6

require (
	github.com/craftgodotdev/craftgo v0.0.0
	github.com/craftgodotdev/craftgo/pkg/events/kafka v0.0.0
	github.com/twmb/franz-go v1.21.6
	github.com/twmb/franz-go/pkg/kfake v0.0.0-20260914031441-1623827d1042
	github.com/twmb/franz-go/pkg/kmsg v1.13.1
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.11
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/klauspost/compress v1.18.7 // indirect
	github.com/pierrec/lz4/v4 v4.1.26 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/craftgodotdev/craftgo/pkg/events v1.8.2
	github.com/craftgodotdev/craftgo/pkg/wire v0.0.0
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
)

replace github.com/craftgodotdev/craftgo => ../../..

replace github.com/craftgodotdev/craftgo/pkg/events => ../../../pkg/events

replace github.com/craftgodotdev/craftgo/pkg/events/kafka => ../../../pkg/events/kafka

replace github.com/craftgodotdev/craftgo/pkg/wire => ../../../pkg/wire

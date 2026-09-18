// The Kafka adapter is its own module so the event runtime - and any
// contract package that depends on it - stays free of a broker client.
module github.com/craftgodotdev/craftgo/pkg/events/kafka

go 1.25.0

require (
	github.com/craftgodotdev/craftgo/pkg/events v1.8.1
	github.com/twmb/franz-go v1.21.6
	github.com/twmb/franz-go/pkg/kfake v0.0.0-20260914031441-1623827d1042
	github.com/twmb/franz-go/pkg/kmsg v1.13.1
)

require (
	github.com/klauspost/compress v1.18.7 // indirect
	github.com/pierrec/lz4/v4 v4.1.26 // indirect
)

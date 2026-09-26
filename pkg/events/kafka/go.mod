// The Kafka adapter is its own module so the event runtime - and any
// contract package that depends on it - stays free of a broker client.
module github.com/craftgodotdev/craftgo/pkg/events/kafka

go 1.26.0

require (
	github.com/craftgodotdev/craftgo/pkg/events v1.10.0
	github.com/twmb/franz-go v1.22.0
	github.com/twmb/franz-go/pkg/kfake v0.0.0-20260914031441-1623827d1042
	github.com/twmb/franz-go/pkg/kmsg v1.14.0
)

require (
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.30 // indirect
	golang.org/x/crypto v0.57.0 // indirect
)

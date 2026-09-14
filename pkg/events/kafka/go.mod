// The Kafka adapter is its own module so the event runtime - and any
// contract package that depends on it - stays free of a broker client.
module github.com/craftgodotdev/craftgo/pkg/events/kafka

go 1.23

replace github.com/craftgodotdev/craftgo/pkg/events => ../

require (
	github.com/craftgodotdev/craftgo/pkg/events v0.0.0-00010101000000-000000000000
	github.com/segmentio/kafka-go v0.4.51
)

require (
	github.com/klauspost/compress v1.15.9 // indirect
	github.com/pierrec/lz4/v4 v4.1.15 // indirect
)

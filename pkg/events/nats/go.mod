// The NATS adapter is its own module so the event runtime - and any
// contract package that depends on it - stays free of a broker client.
module github.com/craftgodotdev/craftgo/pkg/events/nats

go 1.25.0

require (
	github.com/craftgodotdev/craftgo/pkg/events v1.9.0
	github.com/nats-io/nats.go v1.53.1
)

require (
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/nats-io/nkeys v0.4.16 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

// The event runtime is its own module, so a generated contract package does not pull in craftgo's root module.
// At this go version one `for` variable is shared by every iteration: copy it before a closure captures it.
module github.com/craftgodotdev/craftgo/pkg/events

go 1.26.0

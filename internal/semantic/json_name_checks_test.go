package semantic

import "testing"

func TestJSONKeyMayDifferFromTheFieldName(t *testing.T) {
	expectClean(t, `type Item { id string }
type Order { items Item[] @json("OrderItem")  storeId string @json("store_id") }`)
}

func TestTwoFieldsMayNotShareAJSONKey(t *testing.T) {
	expectError(t, `type Order { a string @json("id")  id string }`, CodeFieldNameCollision)
}

func TestJSONKeyMustBeAPlainKey(t *testing.T) {
	expectError(t, `type Order { a string @json("") }`, CodeDecoratorArgValue)
	expectError(t, `type Order { a string @json("has space") }`, CodeDecoratorArgValue)
}

func TestJSONDoesNotCombineWithAnOffBodyBinding(t *testing.T) {
	expectError(t, `type Req { page int? @query @json("p") }
service S { post Do /do { request Req } }`, CodeDecoratorConflict)
}

func TestJSONDoesNotCombineWithSensitive(t *testing.T) {
	expectError(t, `type Req { secret string @sensitive @json("s") }`, CodeDecoratorConflict)
}

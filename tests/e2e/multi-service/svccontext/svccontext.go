// Package svccontext is the shared dependency container for both services.
package svccontext

// ServiceContext holds whatever both services need at runtime. The
// multi-service e2e fixture seeds it with stable demo values so HTTP
// assertions stay deterministic.
type ServiceContext struct {
	OrderID    string
	OrderTotal int
	ItemSKU    string
	ItemPrice  int
}

// NewServiceContext returns a ServiceContext seeded with demo data.
func NewServiceContext() *ServiceContext {
	return &ServiceContext{
		OrderID: "ord-1", OrderTotal: 9900,
		ItemSKU: "sku-1", ItemPrice: 1990,
	}
}

package svccontext

// ServiceContext is the cornercase fixture's dependency container.
// Most cornercase methods do not need state — the scaffold exists
// solely so the generated routes compile.
type ServiceContext struct {
	Middlewares
}

// NewServiceContext returns an empty ServiceContext.
func NewServiceContext() *ServiceContext {
	return &ServiceContext{}
}

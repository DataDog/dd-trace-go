package internal

import "sync/atomic"

var (
	// globalTracer stores the current tracer as *ddtrace/tracer.Tracer (pointer to interface). The
	// atomic.Value type requires types to be consistent, which requires using the same type for the
	// stored value.
	globalTracer atomic.Value
)

// tracerLike is an interface to restrict the types that can be stored in `globalTracer`.
// This interface doesn't leak to the users. We are leveraging the type system to generating
// the functions below for `tracer.Tracer` without creating an import cycle.
type tracerLike interface {
	Flush()
	Stop()
}

// StoreGlobalTracer stores a tracer in the global tracer.
// It is the responsibility of the caller to ensure that the value is `tracer.Tracer`.
func StoreGlobalTracer[T tracerLike](t T) {
	globalTracer.Store(&t)
}

// SetGlobalTracer sets the global tracer to t.
// It is the responsibility of the caller to ensure that the value is `tracer.Tracer`.
func SetGlobalTracer[T tracerLike](t T) {
	old := *globalTracer.Swap(&t).(*T)
	old.Stop()
}

// GetGlobalTracer returns the current global tracer.
// It is the responsability of the caller to ensure that calling code uses `tracer.Tracer`
// as generic type.
func GetGlobalTracer[T tracerLike]() T {
	return *globalTracer.Load().(*T)
}

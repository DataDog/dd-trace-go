package pgx

type Option func(*tracer)

func WithServiceName(name string) Option {
	return func(t *tracer) {
		t.serviceName = name
	}
}

func WithTraceBatch() Option {
	return func(t *tracer) {
		t.traceBatch = true
	}
}

func WithTraceCopyFrom() Option {
	return func(t *tracer) {
		t.traceCopyFrom = true
	}
}

func WithTracePrepare() Option {
	return func(t *tracer) {
		t.tracePrepare = true
	}
}

func WithTraceConnect() Option {
	return func(t *tracer) {
		t.traceConnect = true
	}
}

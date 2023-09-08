package pgx

type Option func(*tracer)

// WithServiceName sets the service name to use for all spans.
func WithServiceName(name string) Option {
	return func(t *tracer) {
		t.serviceName = name
	}
}

// WithTraceBatch enables tracing batched operations (i.e. pgx.Batch{}).
func WithTraceBatch() Option {
	return func(t *tracer) {
		t.traceBatch = true
	}
}

// WithTraceCopyFrom enables tracing pgx.CopyFrom calls.
func WithTraceCopyFrom() Option {
	return func(t *tracer) {
		t.traceCopyFrom = true
	}
}

// WithTracePrepare enables tracing prepared statements.
//
//	conn, err := pgx.Connect(ctx, "postgres://user:pass@example.com:5432/dbname", pgx.WithTraceConnect())
//	if err != nil {
//		// handle err
//	}
//	defer conn.Close(ctx)
//
//	_, err := conn.Prepare(ctx, "stmt", "select $1::integer")
//	row, err := conn.QueryRow(ctx, "stmt", 1)
func WithTracePrepare() Option {
	return func(t *tracer) {
		t.tracePrepare = true
	}
}

// WithTraceConnect enables tracing calls to Connect and ConnectConfig.
//
//	pgx.Connect(ctx, "postgres://user:pass@example.com:5432/dbname", pgx.WithTraceConnect())
func WithTraceConnect() Option {
	return func(t *tracer) {
		t.traceConnect = true
	}
}

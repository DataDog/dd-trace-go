package pgx

import (
	"context"

	"github.com/jackc/pgx/v5"
)

func Connect(ctx context.Context, connString string, opts ...Option) (*pgx.Conn, error) {
	connConfig, err := pgx.ParseConfig(connString)
	if err != nil {
		return nil, err
	}

	return ConnectConfig(ctx, connConfig, opts...)
}

func ConnectConfig(ctx context.Context, connConfig *pgx.ConnConfig, opts ...Option) (*pgx.Conn, error) {
	// The tracer must be set in the config before calling connect
	// as pgx takes ownership of the config. QueryTracer traces
	// may work, but none of the others will, as they're set in
	// unexported fields in the config in the pgx.connect function.
	connConfig.Tracer = New(opts...)

	return pgx.ConnectConfig(ctx, connConfig)
}

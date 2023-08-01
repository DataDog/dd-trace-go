package pgxpool

import (
	"context"

	pgxtracer "gopkg.in/DataDog/dd-trace-go.v1/contrib/jackc/pgx/v5"

	"github.com/jackc/pgx/v5/pgxpool"
)

func New(ctx context.Context, connString string, opts ...pgxtracer.Option) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, err
	}

	return NewWithConfig(ctx, config, opts...)
}

func NewWithConfig(ctx context.Context, config *pgxpool.Config, opts ...pgxtracer.Option) (*pgxpool.Pool, error) {
	config.ConnConfig.Tracer = pgxtracer.New(opts...)

	return pgxpool.NewWithConfig(ctx, config)
}

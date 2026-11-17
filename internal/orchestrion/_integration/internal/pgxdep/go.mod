// A module outside dd-trace-go's paths that opens pgx connections and pools,
// standing in for a third-party library. otelc must instrument call sites in
// dependencies, not only in the module being built.
module example.com/pgxdep

go 1.26.0

require github.com/jackc/pgx/v5 v5.9.2

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

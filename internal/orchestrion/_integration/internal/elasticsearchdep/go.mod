// A module outside dd-trace-go's paths that creates go-elasticsearch clients,
// standing in for a third-party library. otelc must instrument call sites in
// dependencies, not only in the module being built.
module example.com/elasticsearchdep

go 1.26.0

require (
	github.com/elastic/go-elasticsearch/v6 v6.8.5
	github.com/elastic/go-elasticsearch/v7 v7.17.1
	github.com/elastic/go-elasticsearch/v8 v8.12.1
)

require (
	github.com/elastic/elastic-transport-go/v8 v8.4.0 // indirect
	github.com/go-logr/logr v1.3.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	go.opentelemetry.io/otel v1.21.0 // indirect
	go.opentelemetry.io/otel/metric v1.21.0 // indirect
	go.opentelemetry.io/otel/trace v1.21.0 // indirect
)

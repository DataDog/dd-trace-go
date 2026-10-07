// A module outside dd-trace-go's paths that creates gqlgen servers, standing in
// for a third-party library. otelc must rewrite call sites in dependencies, not
// only in the module being built.
module example.com/gqlgendep

go 1.26.0

require github.com/99designs/gqlgen v0.17.92

require (
	github.com/agnivade/levenshtein v1.2.1 // indirect
	github.com/coder/websocket v1.8.15 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/sosodev/duration v1.4.0 // indirect
	github.com/vektah/gqlparser/v2 v2.5.35 // indirect
	golang.org/x/sync v0.21.0 // indirect
)

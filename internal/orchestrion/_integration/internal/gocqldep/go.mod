// A module outside dd-trace-go's paths that builds gocql clusters and
// sessions, standing in for a third-party library. otelc must instrument call
// sites in dependencies, not only in the module being built.
module example.com/gocqldep

go 1.26.0

require github.com/gocql/gocql v1.7.0

require (
	github.com/golang/snappy v0.0.3 // indirect
	github.com/hailocab/go-hostpool v0.0.0-20160125115350-e80d13ce29ed // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
)

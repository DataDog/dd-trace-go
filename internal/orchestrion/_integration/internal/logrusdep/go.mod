// A module outside dd-trace-go's paths that builds logrus loggers, standing in
// for a third-party library. otelc must instrument struct literals in
// dependencies, not only in the module being built.
module example.com/logrusdep

go 1.26.0

require github.com/sirupsen/logrus v1.9.4

require golang.org/x/sys v0.13.0 // indirect

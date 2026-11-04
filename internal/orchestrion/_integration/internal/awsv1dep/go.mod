// A module outside dd-trace-go's paths that creates aws-sdk-go sessions,
// standing in for a third-party library. otelc must instrument call sites in
// dependencies, not only in the module being built.
module example.com/awsv1dep

go 1.26.0

require github.com/aws/aws-sdk-go v1.55.6

require github.com/jmespath/go-jmespath v0.4.0 // indirect

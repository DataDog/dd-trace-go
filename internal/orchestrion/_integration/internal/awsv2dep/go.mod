// A module outside dd-trace-go's paths that builds aws.Config values, standing
// in for a third-party library. otelc must instrument them in dependencies, not
// only in the module being built.
module example.com/awsv2dep

go 1.26.0

require github.com/aws/aws-sdk-go-v2 v1.43.2

require github.com/aws/smithy-go v1.27.5 // indirect

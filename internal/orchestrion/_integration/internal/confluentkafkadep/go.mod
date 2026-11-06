// A module outside dd-trace-go's paths that creates confluent-kafka-go
// clients, standing in for a third-party library. otelc must instrument call
// sites in dependencies, not only in the module being built.
module example.com/confluentkafkadep

go 1.26.0

require (
	github.com/confluentinc/confluent-kafka-go v1.9.2
	github.com/confluentinc/confluent-kafka-go/v2 v2.15.0
)

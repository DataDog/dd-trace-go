package instrumentation

type Package string

const (
	Package99DesignsGQLGen      Package = "99designs/gqlgen"
	PackageAWSSDKGo             Package = "aws/aws-sdk-go"
	PackageAWSSDKGoV2           Package = "aws/aws-sdk-go-v2"
	PackageBradfitzMemcache     Package = "bradfitz/gomemcache/memcache"
	PackageCloudGoogleComPubsub Package = "cloud.google.com/go/pubsub.v1"
	PackageConfluentKafkaGo     Package = "confluentinc/confluent-kafka-go/kafka"
	PackageConfluentKafkaGoV2   Package = "confluentinc/confluent-kafka-go/kafka.v2"

	// TODO: ...

	PackageNetHTTP   Package = "net/http"
	PackageIBMSarama Package = "IBM/sarama"
)

type Component int

const (
	ComponentDefault Component = iota
	ComponentServer
	ComponentClient
)

type PackageInfo struct {
	external bool

	TracedPackage string
}

func RegisterPackage(name string, info PackageInfo) error {
	info.external = true
	return nil
}

var packages = map[Package]PackageInfo{
	Package99DesignsGQLGen: {},
	PackageAWSSDKGo:        {},
}

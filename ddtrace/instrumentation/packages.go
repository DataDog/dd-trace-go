package instrumentation

type Package string

const (
	Package99DesignsGQLGen      = "99designs/gqlgen"
	PackageAWSSDKGo             = "aws/aws-sdk-go"
	PackageAWSSDKGoV2           = "aws/aws-sdk-go-v2"
	PackageBradfitzMemcache     = "bradfitz/gomemcache/memcache"
	PackageCloudGoogleComPubsub = "cloud.google.com/go/pubsub.v1"
	PackageConfluentKafkaGo     = "confluentinc/confluent-kafka-go/kafka"
	PackageConfluentKafkaGoV2   = "confluentinc/confluent-kafka-go/kafka.v2"

	// TODO: ...

	PackageNetHTTP   = "net/http"
	PackageIBMSarama = "IBM/sarama"
)

type NamingSchema struct {
}

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

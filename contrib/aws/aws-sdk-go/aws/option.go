package aws // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go/aws"

const (
	awsAgentTag     = "aws.agent"
	awsOperationTag = "aws.operation"
	awsRegionTag    = "aws.region"
)

type config struct {
	// when set to the empty string, the service name will be inferred based on
	// the request to AWS
	serviceName string
}

// Option represents an option that can be passed to Dial.
type Option func(*config)

func defaults(cfg *config) {
}

// WithServiceName sets the given service name for the dialled connection.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

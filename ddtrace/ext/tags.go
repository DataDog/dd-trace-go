// Package ext contains a set of Datadog-specific constants. Most of them are used
// for setting span metadata.
package ext

const (
	// TargetHost sets the target host address.
	TargetHost = "out.host"

	// TargetPort sets the target host port.
	TargetPort = "out.port"

	// SamplingPriority is the tag that marks the sampling priority of a span.
	SamplingPriority = "sampling.priority"

	// SQLType sets the sql type tag.
	SQLType = "sql"

	// SQLQuery sets the sql query tag on a span.
	SQLQuery = "sql.query"

	// HTTPMethod specifies the HTTP method used in a span.
	HTTPMethod = "http.method"

	// HTTPCode sets the HTTP status code as a tag.
	HTTPCode = "http.status_code"

	// HTTPURL sets the HTTP URL for a span.
	HTTPURL = "http.url"

	// TODO: In the next major version, suffix these constants (SpanType, etc)
	// with "*Key" (SpanTypeKey, etc) to more easily differentiate between
	// constants representing tag values and constants representing keys.

	// SpanType defines the Span type (web, db, cache).
	SpanType = "span.type"

	// ServiceName defines the Service name for this Span.
	ServiceName = "service.name"

	// ResourceName defines the Resource name for the Span.
	ResourceName = "resource.name"

	// Error specifies the error tag. It's value is usually of type "error".
	Error = "error"

	// ErrorMsg specifies the error message.
	ErrorMsg = "error.msg"

	// ErrorType specifies the error type.
	ErrorType = "error.type"

	// ErrorStack specifies the stack dump.
	ErrorStack = "error.stack"

	// Environment specifies the environment to use with a trace.
	Environment = "env"

	// PeerHostIPV4 records IPv4 host address of the peer.
	PeerHostIPV4 = "peer.ipv4"
	// PeerHostIPV6 records the IPv6 host address of the peer.
	PeerHostIPV6 = "peer.ipv6"
	// PeerService records the service name of the peer service.
	PeerService = "peer.service"
	// PeerHostname records the host name of the peer.
	PeerHostname = "peer.hostname"
	// PeerPort records the port number of the peer.
	PeerPort = "peer.port"

	// DBType indicates the type of Database.
	DBType = "db.type"
	// DBInstance indicates the instance name of Database.
	DBInstance = "db.instance"
	// DBUser indicates the user name of Database, e.g. "readonly_user" or "reporting_user".
	DBUser = "db.user"
	// DBStatement records a database statement for the given database type.
	DBStatement = "db.statement"
)

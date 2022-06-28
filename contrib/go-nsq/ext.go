package nsq

const (
	// LocalAddr is the local address to use when dialing an nsqd.
	// If empty, a local address is automatically chosen.
	LocalAddr = "local_addr"

	// Identifiers sent to nsqd representing this client.
	// UserAgent is in the spirit of HTTP (default: "<client_library_name>/<version>").
	ClientID  = "client_id"
	Hostname  = "hostname"
	UserAgent = "user_agent"

	// Integer percentage to sample the channel (requires nsqd 0.2.25+).
	SampleRate = "channel_sample_rate"

	// Compression Settings.
	Deflate      = "deflate"
	DeflateLevel = "deflate_level"
	Snappy       = "snappy"

	// Total message body size and count enqueue.
	MsgCount = "msg_count"
	MsgSize  = "msg_size"

	// Enqueue and dequeue timestamp.
	EnqueueTime = "enqueue_time"
	DequeueTime = "dequeue_time"

	// Enqueue deferred duration.
	DeferredDuration = "deferred_duration"

	// ConsumerStats represents a snapshot of the state of a Consumer's connections and the messages
	// it has seen.
	Connections = "connections"
	MsgReceived = "msg_received"
	MsgFinished = "msg_finished"
	MsgRequeued = "msg_requeued"

	// IsStarved indicates whether any connections for this consumer are blocked on processing
	// before being able to receive more messages (ie. RDY count of 0 and not exiting)
	IsStarved = "is_starved"

	// Message id attempts and timestamp.
	// Nsqd address.
	MsgID        = "msg_id"
	MsgAttempts  = "msg_attempts"
	MsgTimestamp = "msg_timestamp"
	MsgSrcNSQD   = "msg_src_nsqd"

	// The number of goroutines to spawn for message handling.
	Concurrency = "concurrency"
)

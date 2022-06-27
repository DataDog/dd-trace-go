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

	// Enqueue timestamp.
	EnqueueTime = "enqueue_time"

	// Enqueue deferred duration.
	DeferredDuration = "deferred_duration"

	//
	Connections    = "connections"
	NsqMsgReceived = "nsq_msg_received"
	NsqMsgFinished = "nsq_msg_finished"
	NsqMsgRequeued = "nsq_msg_requeued"
	Starved        = "starved"
	MsgID          = "msg_id"
	MsgAttempts    = "msg_attempts"
	MsgTimestamp   = "msg_timestamp"
	MsgSrcNSQD     = "msg_src_nsqd"
	DequeueTime    = "dequeue_time"
	Concurrency    = "concurrency"
)

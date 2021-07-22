package nsq

import (
	"time"

	"github.com/nsqio/go-nsq"
)

type TraceableProducer interface {
	Ping() error
	Publish(topic string, body []byte) error
	MultiPublish(topic string, body [][]byte) error
	PublishAsync(topic string, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error
	MultiPublishAsync(topic string, body [][]byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error
	DeferredPublish(topic string, delay time.Duration, body []byte) error
	DeferredPublishAsync(topic string, delay time.Duration, body []byte, doneChan chan *nsq.ProducerTransaction, args ...interface{}) error
	Stop()
}

type TraceableConsumer interface {
	Stats() *nsq.ConsumerStats
	SetBehaviorDelegate(cb interface{})
	IsStarved() bool
	ChangeMaxInFlight(maxInFlight int)
	ConnectToNSQLookupd(addr string) error
	ConnectToNSQLookupds(addresses []string) error
	ConnectToNSQDs(addresses []string) error
	ConnectToNSQD(addr string) error
	DisconnectFromNSQD(addr string) error
	DisconnectFromNSQLookupd(addr string) error
	AddHandler(handler nsq.Handler)
	AddConcurrentHandlers(handler nsq.Handler, concurrency int)
	Stop()
}

package nsq

import (
	"context"
	"log"
	"net"
	"os"
	"testing"
	"time"

	"github.com/nsqio/go-nsq"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

var (
	lookupdHttpAddr = "127.0.0.1:4161"
	nsqdTcpAddr     = "127.0.0.1:4150"
	nsqdHttpAddr    = "127.0.0.1:4151"
	topic           = "nsq_ddtrace_test"
	channel         = "nsq_ddtrace_test_consumer"
	msgBody         = []byte(`{"service":"nsq_ddtrace"}`)
	multiMsgBody    = [][]byte{msgBody, msgBody, msgBody}
)

type ConsumerHandler struct {
	msgCount int
}

func (this *ConsumerHandler) HandleMessage(msg *nsq.Message) error {
	log.Printf("message count: %d, message body: %s\n", this.msgCount, string(msg.Body))

	return nil
}

func TestProducer(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// tracer.Start(tracer.WithAgentAddr("10.200.7.21:9529"))
	// defer tracer.Stop()

	config := NewConfig(WithService("producer_with_trace_test"), WithResource("nsq_producer"), WithContext(context.Background()))
	config.LocalAddr, _ = net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	producer, err := NewProducer(nsqdTcpAddr, config)
	if err != nil {
		log.Fatalln(err.Error())
	}

	producer.SetMetaTag("test_tag", "producer")

	if err = producer.Ping(); err != nil {
		log.Fatalln(err.Error())
	}

	if err = producer.Publish(topic, msgBody); err != nil {
		log.Fatalln(err.Error())
	}

	if err = producer.MultiPublish(topic, multiMsgBody); err != nil {
		log.Fatalln(err.Error())
	}

	done := make(chan *nsq.ProducerTransaction)
	if err = producer.PublishAsync(topic, msgBody, done); err != nil {
		log.Fatalln(err)
	}
	if trans := <-done; trans.Error != nil {
		log.Fatalln(err.Error())
	}

	if err = producer.MultiPublishAsync(topic, multiMsgBody, done); err != nil {
		log.Fatalln(err.Error())
	}
	if trans := <-done; trans.Error != nil {
		log.Fatalln(err.Error())
	}

	if err = producer.DeferredPublish(topic, time.Second, msgBody); err != nil {
		log.Fatalln(err.Error())
	}

	if err = producer.DeferredPublishAsync(topic, time.Second, msgBody, done); err != nil {
		log.Fatalln(err.Error())
	}
	if trans := <-done; trans.Error != nil {
		log.Fatalln(err.Error())
	}

	producer.Stop()
}

func TestConsumer(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// tracer.Start(tracer.WithAgentAddr("10.200.7.21:9529"))
	// defer tracer.Stop()

	config := NewConfig(WithService("consumer_with_trace_test"), WithResource("nsq_consumer"), WithContext(context.Background()))
	config.LocalAddr, _ = net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	consumer, err := NewConsumer(topic, channel, config)
	if err != nil {
		log.Fatalln(err.Error())
	}

	consumer.SetMetaTag("test_tag", "consumer")

	stats := consumer.Stats()
	log.Printf("connections:%d received:%d requested:%d finished:%d\n", stats.Connections, stats.MessagesReceived, stats.MessagesRequeued, stats.MessagesFinished)

	log.Printf("is starved:%v", consumer.IsStarved())

	consumer.ChangeMaxInFlight(123)

	consumer.AddHandler(&ConsumerHandler{})

	if err = consumer.ConnectToNSQD(nsqdTcpAddr); err != nil {
		log.Fatalln(err.Error())
	}
	if err = consumer.DisconnectFromNSQD(nsqdTcpAddr); err != nil {
		log.Fatalln(err.Error())
	}

	if err = consumer.ConnectToNSQLookupd(lookupdHttpAddr); err != nil {
		log.Fatalln(err.Error())
	}
	// if err = consumer.DisconnectFromNSQLookupd(lookupdHttpAddr); err != nil {
	// 	log.Fatalln(err.Error())
	// }

	consumer.Stop()
	<-consumer.StopChan
}

func init() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}

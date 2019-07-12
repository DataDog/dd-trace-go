// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package mongo

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/mongodb/mongo-go-driver/bson"
	"github.com/mongodb/mongo-go-driver/mongo"
	"github.com/mongodb/mongo-go-driver/mongo/options"
	"github.com/mongodb/mongo-go-driver/x/network/result"
	"github.com/mongodb/mongo-go-driver/x/network/wiremessage"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

func Test(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	li, err := mockMongo()
	if err != nil {
		t.Fatal(err)
	}

	hostname, port, _ := net.SplitHostPort(li.Addr().String())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()

	span, ctx := tracer.StartSpanFromContext(ctx, "mongodb-test")

	addr := fmt.Sprintf("mongodb://%s", li.Addr().String())
	opts := options.Client()
	opts.SetMonitor(NewMonitor())
	opts.SetSingle(true)
	client, err := mongo.Connect(ctx, addr, opts)
	if err != nil {
		t.Fatal(err)
	}

	client.
		Database("test-database").
		Collection("test-collection").
		InsertOne(ctx, bson.D{{Key: "test-item", Value: "test-value"}})

	span.Finish()

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2)
	assert.Equal(t, spans[0].TraceID(), spans[1].TraceID())

	s := spans[0]
	assert.Equal(t, "mongo", s.Tag(ext.ServiceName))
	assert.Equal(t, "mongo.insert", s.Tag(ext.ResourceName))
	assert.Equal(t, hostname, s.Tag(ext.PeerHostname))
	assert.Equal(t, port, s.Tag(ext.PeerPort))
	assert.Contains(t, s.Tag(ext.DBStatement), `"test-item":"test-value"`)
	assert.Equal(t, "test-database", s.Tag(ext.DBInstance))
	assert.Equal(t, "mongo", s.Tag(ext.DBType))
}

// mockMongo implements a crude mongodb server that responds with
// expected replies so that we can confirm tracing works properly
func mockMongo() (net.Listener, error) {
	li, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return li, err
	}

	go func() {
		var requestID int32
		nextRequestID := func() int32 {
			requestID++
			return requestID
		}
		defer li.Close()
		for {
			conn, err := li.Accept()
			if err != nil {
				break
			}
			go func() {
				defer conn.Close()

				for {
					var hdrbuf [16]byte
					_, err := io.ReadFull(conn, hdrbuf[:])
					if err != nil {
						panic(err)
					}

					hdr, err := wiremessage.ReadHeader(hdrbuf[:], 0)
					if err != nil {
						panic(err)
					}

					msgbuf := make([]byte, hdr.MessageLength)
					copy(msgbuf, hdrbuf[:])
					_, err = io.ReadFull(conn, msgbuf[16:])
					if err != nil {
						panic(err)
					}

					switch hdr.OpCode {
					case wiremessage.OpQuery:
						var msg wiremessage.Query
						err = msg.UnmarshalWireMessage(msgbuf)
						if err != nil {
							panic(err)
						}

						bs, _ := bson.Marshal(result.IsMaster{
							IsMaster:                     true,
							OK:                           1,
							MaxBSONObjectSize:            16777216,
							MaxMessageSizeBytes:          48000000,
							MaxWriteBatchSize:            100000,
							LogicalSessionTimeoutMinutes: 30,
							ReadOnly:                     false,
							MinWireVersion:               0,
							MaxWireVersion:               7,
						})

						reply := wiremessage.Reply{
							MsgHeader: wiremessage.Header{
								RequestID:  nextRequestID(),
								ResponseTo: hdr.RequestID,
							},
							ResponseFlags:  wiremessage.AwaitCapable,
							NumberReturned: 1,
							Documents:      []bson.Raw{bs},
						}
						bs, err = reply.MarshalWireMessage()
						if err != nil {
							panic(err)
						}

						_, err = conn.Write(bs)
						if err != nil {
							panic(err)
						}

					case wiremessage.OpMsg:
						var msg wiremessage.Msg
						err = msg.UnmarshalWireMessage(msgbuf)
						if err != nil {
							panic(err)
						}
						d := bson.D{{Key: "n", Value: 1}, {Key: "ok", Value: 1}}
						bs, _ := bson.Marshal(d)

						bs, _ = wiremessage.Msg{
							MsgHeader: wiremessage.Header{
								RequestID:  nextRequestID(),
								ResponseTo: hdr.RequestID,
							},
							Sections: []wiremessage.Section{
								&wiremessage.SectionBody{
									Document: bs,
								},
							},
						}.MarshalWireMessage()

						_, err = conn.Write(bs)
						if err != nil {
							panic(err)
						}

					default:
						panic("unknown op code: " + hdr.OpCode.String())
					}

				}
			}()
		}
	}()

	return li, nil
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...Option) {
		li, err := mockMongo()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
		defer cancel()

		addr := fmt.Sprintf("mongodb://%s", li.Addr().String())
		mongopts := options.Client()
		mongopts.SetMonitor(NewMonitor(opts...))
		mongopts.SetSingle(true)
		client, err := mongo.Connect(ctx, addr, mongopts)
		if err != nil {
			t.Fatal(err)
		}
		client.
			Database("test-database").
			Collection("test-collection").
			InsertOne(ctx, bson.D{{Key: "test-item", Value: "test-value"}})

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		s := spans[0]
		assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})
}

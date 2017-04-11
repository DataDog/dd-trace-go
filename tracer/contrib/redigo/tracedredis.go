package tracedredis

import (
	"context"
	"github.com/DataDog/dd-trace-go/tracer"
	redis "github.com/garyburd/redigo/redis"
	"strconv"
	"strings"
)

type TracedConn struct {
	redis.Conn
	tracer  *tracer.Tracer
	network string
	host    string
	port    string
}

func TracedDial(tracer *tracer.Tracer, network, address string, options ...redis.DialOption) (TracedConn, error) {
	c, err := redis.Dial(network, address)
	addr := strings.Split(address, ":")
	host := addr[0]
	port := addr[1]
	tc := TracedConn{c, tracer, network, host, port}
	return tc, err
}

func (tc TracedConn) TraceDo(ctx context.Context, commandName string, args ...interface{}) (reply interface{}, err error) {
	span := tc.tracer.NewChildSpanFromContext("redis.command", ctx)
	span.SetMeta("out.network", tc.network)
	span.SetMeta("out.port", tc.port)
	span.SetMeta("out.host", tc.host)
	span.SetMeta("redis.args_length", strconv.Itoa(len(args)))

	raw_command := commandName
	for _, arg := range args {
		raw_command += " " + arg.(string)
	}
	span.SetMeta("redis.raw_command", raw_command)
	ret, err := tc.Do(commandName, args...)

	if err != nil {
		span.SetError(err)
	}
	span.Finish()

	return ret, err
}

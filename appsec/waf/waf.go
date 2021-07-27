package waf

import (
	"fmt"
	"github.com/sqreen/go-libsqreen/waf/types"
	"gopkg.in/DataDog/dd-trace-go.v1/appsec/dyngo"
	httpinstr "gopkg.in/DataDog/dd-trace-go.v1/appsec/instrumentation/http"
	"time"

	"github.com/sqreen/go-libsqreen/waf"
)

func NewOperationEventListener() dyngo.EventListener {
	subscriptions := []string{
		"server.request.query",
		"server.request.headers.no_cookies",
	}
	wafRule, err := waf.NewRule(staticWAFRule)
	if err != nil {
		panic(err)
	}
	return dyngo.OnStartEventListener(func(op *dyngo.Operation, args httpinstr.HandlerOperationArgs) {
		// For this handler operation lifetime, create a WAF context and set of the data seen during it
		wafCtx := waf.NewAdditiveContext(wafRule)
		set := types.DataSet{}
		op.OnFinish(func(op *dyngo.Operation, _ httpinstr.HandlerOperationRes) {
			wafCtx.Close()
		})

		subscribe(op, subscriptions, wafCtx, set)
	})
}

func runWAF(wafCtx types.Rule, set types.DataSet) {
	action, info, err := wafCtx.Run(set, 5*time.Millisecond)
	if err != nil {
		panic(err)
	}
	if action != types.NoAction {
		panic(fmt.Errorf("attack found %v", string(info)))
	}
}

func subscribe(op *dyngo.Operation, subscriptions []string, wafCtx types.Rule, set types.DataSet) {
	run := func(addr string, v interface{}) {
		set[addr] = v
		runWAF(wafCtx, set)
	}
	for _, addr := range subscriptions {
		addr := addr
		switch addr {
		case "http.user_agent":
			op.OnData(func(_ *dyngo.Operation, data httpinstr.UserAgent) {
				run(addr, data)
			})
		case "server.request.headers.no_cookies":
			op.OnData(func(_ *dyngo.Operation, data httpinstr.Header) {
				run(addr, data)
			})
		case "server.request.query":
			op.OnData(func(_ *dyngo.Operation, data httpinstr.QueryValues) {
				run(addr, data)
			})
		}
	}
}

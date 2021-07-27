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
		"http.user_agent",
		"server.request.headers.no_cookies",
	}
	wafRule, err := waf.NewRule(staticWAFRule)
	if err != nil {
		panic(err)
	}
	return dyngo.OnStartEventListener(func(op *dyngo.Operation, args httpinstr.HandlerOperationArgs) {
		wafCtx := waf.NewAdditiveContext(wafRule)
		set := types.DataSet{}
		if len(args.Headers) > 0 {
			set["server.request.headers.no_cookies"] = args.Headers
		}
		set["server.request.query"] = args.URL.Query()
		runWAF(wafCtx, set)

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
	for _, addr := range subscriptions {
		switch addr {
		case "http.user_agent":
			op.OnData(func(op *dyngo.Operation, q httpinstr.UserAgent) {
				set[addr] = q
				runWAF(wafCtx, set)
			})
		case "server.request.headers.no_cookies":
			op.OnData(func(op *dyngo.Operation, h httpinstr.Header) {
				set[addr] = h
				runWAF(wafCtx, set)
			})
		}
	}
}

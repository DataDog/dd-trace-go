package httpsec

import "net/http"

type wrapHandlerCfg struct {
	onBlock            []func()
	responseHdrFetcher func(http.ResponseWriter) http.Header
}
type WrapHandlerOption func(*wrapHandlerCfg)

func defaultWrapHandlerCfg() *wrapHandlerCfg {
	return &wrapHandlerCfg{
		onBlock: []func(){},
		responseHdrFetcher: func(w http.ResponseWriter) http.Header {
			return w.Header()
		},
	}
}

func WithOnBlock(f ...func()) WrapHandlerOption {
	return func(cfg *wrapHandlerCfg) {
		cfg.onBlock = append(cfg.onBlock, f...)
	}
}

func WithResponseHdrFetcher(f func(http.ResponseWriter) http.Header) WrapHandlerOption {
	return func(cfg *wrapHandlerCfg) {
		cfg.responseHdrFetcher = f
	}
}

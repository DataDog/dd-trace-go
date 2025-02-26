package river

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// jsonCarrier is like [tracer.TextMapCarrier], but it carries json data.
//
// We use this to inject and extract job Metadata.
// According to https://github.com/riverqueue/river/blob/fce1b076f6170054a8e4827450856f99183f1e8b/rivertype/river_type.go#L97-L100,
// the job Metadata should always be a valid JSON object payload.
type jsonCarrier map[string]any

var (
	_ tracer.TextMapWriter = (*jsonCarrier)(nil)
	_ tracer.TextMapReader = (*jsonCarrier)(nil)
)

// Set implements [tracer.TextMapWriter].
func (c jsonCarrier) Set(key, val string) {
	c[key] = val
}

// ForeachKey conforms to the [tracer.TextMapReader] interface.
func (c jsonCarrier) ForeachKey(handler func(key, val string) error) error {
	for k, v := range c {
		vstr, ok := v.(string)
		if !ok {
			continue
		}
		if err := handler(k, vstr); err != nil {
			return err
		}
	}
	return nil
}

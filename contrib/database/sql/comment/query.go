package comment

import (
	"fmt"
	"strings"
)

type QueryTextCarrier strings.Builder

// Set implements TextMapWriter.
func (c QueryTextCarrier) Set(key, val string) {
	b := strings.Builder(c)
	if b.Len() == 0 {
		b.WriteString("/* ")
	}

	if !strings.HasSuffix(b.String(), ",") {
		b.WriteRune(',')
	}

	b.WriteString(serializeTag(key, val))
}

func (c QueryTextCarrier) CommentedQuery(query string) (commented string) {
	builder := strings.Builder(c)
	if builder.Len() > 0 {
		builder.WriteString(" */")
	}
	comment := builder.String()

	if comment == "" || query == "" {
		return query
	}

	return fmt.Sprintf("%s %s", comment, query)
}

// ForeachKey implements TextMapReader.
func (c QueryTextCarrier) ForeachKey(handler func(key, val string) error) error {
	// TODO: implement this for completeness. We don't really have a use-case for this at the moment.
	return nil
}

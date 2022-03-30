package tracer

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type SQLCommentCarrier struct {
	tags map[string]string
}

func commentWithTags(tags map[string]string) (comment string) {
	if len(tags) == 0 {
		return ""
	}

	serializedTags := make([]string, 0, len(tags))
	for k, v := range tags {
		serializedTags = append(serializedTags, serializeTag(k, v))
	}

	sort.Strings(serializedTags)
	comment = strings.Join(serializedTags, ",")
	return fmt.Sprintf("/* %s */", comment)
}

// Set implements TextMapWriter.
func (c *SQLCommentCarrier) Set(key, val string) {
	if c.tags == nil {
		c.tags = make(map[string]string)
	}
	c.tags[key] = val
}

func (c *SQLCommentCarrier) CommentedQuery(query string) (commented string) {
	comment := commentWithTags(c.tags)

	if comment == "" || query == "" {
		return query
	}

	return fmt.Sprintf("%s %s", comment, query)
}

// ForeachKey implements TextMapReader.
func (c SQLCommentCarrier) ForeachKey(handler func(key, val string) error) error {
	// TODO: implement this for completeness. We don't really have a use-case for this at the moment.
	return nil
}

func serializeTag(key string, value string) (serialized string) {
	sKey := serializeKey(key)
	sValue := serializeValue(value)

	return fmt.Sprintf("%s=%s", sKey, sValue)
}

func serializeKey(key string) (encoded string) {
	urlEncoded := url.PathEscape(key)
	escapedMeta := escapeMetaChars(urlEncoded)

	return escapedMeta
}

func serializeValue(value string) (encoded string) {
	urlEncoded := url.PathEscape(value)
	escapedMeta := escapeMetaChars(urlEncoded)
	escaped := escapeSQL(escapedMeta)

	return escaped
}

func escapeSQL(value string) (escaped string) {
	return fmt.Sprintf("'%s'", value)
}

func escapeMetaChars(value string) (escaped string) {
	return strings.ReplaceAll(value, "'", "\\'")
}

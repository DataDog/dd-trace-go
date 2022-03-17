package comment

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

func OnQuery(query string, tagSets ...map[string]string) (commentedQuery string) {
	// Don't comment the query if there's no query to tag
	if len(query) == 0 {
		return query
	}

	comment := WithTags(tagSets...)
	if len(comment) == 0 {
		return query
	}

	return fmt.Sprintf("%s %s", query, comment)
}

// totalLen returns the maximum total number of elements in all maps.
// Duplicate keys are counted as multiple elements
func totalLen(tagSets ...map[string]string) (length int) {
	length = 0
	for _, t := range tagSets {
		length += len(t)
	}

	return length
}

func WithTags(tagSets ...map[string]string) (comment string) {
	tagCount := totalLen(tagSets...)
	if tagCount == 0 {
		return ""
	}

	serializedTags := make([]string, 0, tagCount)
	for _, ts := range tagSets {
		for k, v := range ts {
			serializedTags = append(serializedTags, serializeTag(k, v))
		}
	}

	sort.Strings(serializedTags)
	comment = strings.Join(serializedTags, ",")
	return fmt.Sprintf("/* %s */", comment)
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

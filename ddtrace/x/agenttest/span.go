package agenttest

// Span holds the data of a single collected span. Meta and Metrics contain the
// raw string and numeric tags respectively; Tags is a merged view of both plus
// top-level attributes (name, service, resource, type) for convenience.
type Span struct {
	SpanID    uint64
	TraceID   uint64
	ParentID  uint64
	Service   string
	Operation string
	Resource  string
	Type      string
	Start     int64 // unix nanoseconds
	Duration  int64 // nanoseconds
	Error     int32
	Meta      map[string]string
	Metrics   map[string]float64
	Tags      map[string]any // merged view: meta + metrics + top-level attrs
	Children  []*Span
}

// SpanMatch is a builder for span matching conditions. Create one with [With]
// and chain methods to add conditions. Pass the result to [Agent.FindSpan] or
// [Agent.RequireSpan].
type SpanMatch struct {
	conditions []func(*Span) bool
}

// With returns a new empty SpanMatch builder. Chain methods like Service,
// Operation, Tag, etc. to add matching conditions.
func With() *SpanMatch {
	return &SpanMatch{}
}

// Service adds a condition that the span's service must equal the given value.
func (m *SpanMatch) Service(service string) *SpanMatch {
	m.conditions = append(m.conditions, func(s *Span) bool {
		return s.Service == service
	})
	return m
}

// Operation adds a condition that the span's operation name must equal the given value.
func (m *SpanMatch) Operation(operation string) *SpanMatch {
	m.conditions = append(m.conditions, func(s *Span) bool {
		return s.Operation == operation
	})
	return m
}

// Resource adds a condition that the span's resource must equal the given value.
func (m *SpanMatch) Resource(resource string) *SpanMatch {
	m.conditions = append(m.conditions, func(s *Span) bool {
		return s.Resource == resource
	})
	return m
}

// Type adds a condition that the span's type must equal the given value.
func (m *SpanMatch) Type(spanType string) *SpanMatch {
	m.conditions = append(m.conditions, func(s *Span) bool {
		return s.Type == spanType
	})
	return m
}

// Tag adds a condition that the span's merged Tags map must contain the given
// key with the given value.
func (m *SpanMatch) Tag(key string, value any) *SpanMatch {
	m.conditions = append(m.conditions, func(s *Span) bool {
		v, ok := s.Tags[key]
		return ok && v == value
	})
	return m
}

// ParentOf adds a condition that the span's parent ID must equal the given value.
func (m *SpanMatch) ParentOf(parentID uint64) *SpanMatch {
	m.conditions = append(m.conditions, func(s *Span) bool {
		return s.ParentID == parentID
	})
	return m
}

// Matches reports whether the span satisfies all conditions in this SpanMatch.
func (m *SpanMatch) Matches(s *Span) bool {
	for _, c := range m.conditions {
		if !c(s) {
			return false
		}
	}
	return true
}

package agenttest

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

type SpanMatch struct {
	conditions []func(*Span) bool
}

func With() *SpanMatch {
	return &SpanMatch{}
}

func (m *SpanMatch) Service(service string) *SpanMatch {
	m.conditions = append(m.conditions, func(s *Span) bool {
		return s.Service == service
	})
	return m
}

func (m *SpanMatch) Operation(operation string) *SpanMatch {
	m.conditions = append(m.conditions, func(s *Span) bool {
		return s.Operation == operation
	})
	return m
}

func (m *SpanMatch) Resource(resource string) *SpanMatch {
	m.conditions = append(m.conditions, func(s *Span) bool {
		return s.Resource == resource
	})
	return m
}

func (m *SpanMatch) Type(spanType string) *SpanMatch {
	m.conditions = append(m.conditions, func(s *Span) bool {
		return s.Type == spanType
	})
	return m
}

func (m *SpanMatch) Tag(key string, value any) *SpanMatch {
	m.conditions = append(m.conditions, func(s *Span) bool {
		v, ok := s.Tags[key]
		return ok && v == value
	})
	return m
}

func (m *SpanMatch) ParentOf(parentID uint64) *SpanMatch {
	m.conditions = append(m.conditions, func(s *Span) bool {
		return s.ParentID == parentID
	})
	return m
}

func (m *SpanMatch) Matches(s *Span) bool {
	for _, c := range m.conditions {
		if !c(s) {
			return false
		}
	}
	return true
}

package fastdelta

type IntSet struct {
	sparse map[int]struct{}
}

const maxGap = 2

func (s *IntSet) Reset() {
	if s.sparse == nil {
		s.sparse = make(map[int]struct{})
	}
	for k := range s.sparse {
		delete(s.sparse, k)
	}
}

func (s *IntSet) Add(i int) {
	s.sparse[i] = struct{}{}
}

func (s *IntSet) Contains(i int) bool {
	_, ok := s.sparse[i]
	return ok
}

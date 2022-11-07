package fastdelta

type SparseIntSet struct {
	members map[int]struct{}
}

func (s *SparseIntSet) Reset() {
	if s.members == nil {
		s.members = make(map[int]struct{})
	}
	for k := range s.members {
		delete(s.members, k)
	}
}

func (s *SparseIntSet) Add(i int) {
	s.members[i] = struct{}{}
}

func (s *SparseIntSet) Contains(i int) bool {
	_, ok := s.members[i]
	return ok
}

type DenseIntSet struct {
	members []bool
}

func (d *DenseIntSet) Reset() {
	d.members = d.members[:0]
}

func (d *DenseIntSet) Append(val bool) {
	d.members = append(d.members, val)
}

func (d *DenseIntSet) Add(i int) bool {
	if i < 0 || i >= len(d.members) {
		return false
	}
	d.members[i] = true
	return true
}

func (d *DenseIntSet) Contains(i int) bool {
	if i < 0 || i >= len(d.members) {
		return false
	}
	return d.members[i]
}

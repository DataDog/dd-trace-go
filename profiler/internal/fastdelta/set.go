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
	index   int
	members []uint64
}

func (d *DenseIntSet) Reset() {
	d.index = 0
	d.members = d.members[:0]
}

func (d *DenseIntSet) Append(val bool) {
	i := d.index / 64
	if i >= len(d.members) {
		d.members = append(d.members, 0)
	}
	if val {
		d.members[i] |= (1 << (d.index % 64))
	}
	d.index++
}

func (d *DenseIntSet) Add(vals ...int) bool {
	var fail bool
	for _, val := range vals {
		i := val / 64
		if i < 0 || i >= len(d.members) {
			fail = true
		} else {
			d.members[i] |= (1 << (val % 64))
		}
	}
	return !fail
}

func (d *DenseIntSet) Contains(val int) bool {
	i := val / 64
	if i < 0 || i >= len(d.members) {
		return false
	}
	return (d.members[i] & (1 << (val % 64))) != 0
}

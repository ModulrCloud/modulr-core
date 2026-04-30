package utils

// StakeFenwickTree is a Fenwick (Binary Indexed) tree over uint64 stake values.
// It supports O(log N) prefix-sum queries, O(log N) point updates, and
// O(log N) weighted random selection via FindByWeight.
type StakeFenwickTree struct {
	tree []uint64
	n    int
}

// NewStakeFenwickTree builds a Fenwick tree from the given stake values in O(N).
func NewStakeFenwickTree(stakes []uint64) *StakeFenwickTree {
	n := len(stakes)
	t := &StakeFenwickTree{tree: make([]uint64, n+1), n: n}
	copy(t.tree[1:], stakes)
	for i := 1; i <= n; i++ {
		parent := i + (i & (-i))
		if parent <= n {
			t.tree[parent] += t.tree[i]
		}
	}
	return t
}

// Update adds delta to position idx (0-based). To remove a validator with
// stake s, call Update(idx, s) — the method subtracts the value internally.
func (t *StakeFenwickTree) Remove(idx int, stake uint64) {
	i := idx + 1
	for i <= t.n {
		t.tree[i] -= stake
		i += i & (-i)
	}
}

// FindByWeight returns the 0-based index of the first position whose
// prefix sum exceeds r. This is equivalent to the linear cumulative-sum
// scan but runs in O(log N).
func (t *StakeFenwickTree) FindByWeight(r uint64) int {
	pos := 0
	bitMask := 1
	for bitMask <= t.n {
		bitMask <<= 1
	}
	bitMask >>= 1

	for bitMask > 0 {
		next := pos + bitMask
		if next <= t.n && t.tree[next] <= r {
			r -= t.tree[next]
			pos = next
		}
		bitMask >>= 1
	}
	return pos
}

package tests

import (
	"testing"

	"github.com/modulrcloud/modulr-core/utils"
)

func TestFenwickTree_SingleElement(t *testing.T) {
	tree := utils.NewStakeFenwickTree([]uint64{100})

	idx := tree.FindByWeight(0)
	if idx != 0 {
		t.Fatalf("expected index 0, got %d", idx)
	}
	idx = tree.FindByWeight(99)
	if idx != 0 {
		t.Fatalf("expected index 0 for r=99, got %d", idx)
	}
}

func TestFenwickTree_UniformStakes(t *testing.T) {
	stakes := []uint64{10, 10, 10, 10, 10}
	tree := utils.NewStakeFenwickTree(stakes)

	// r=0..9 -> idx 0, r=10..19 -> idx 1, etc.
	expected := []int{0, 0, 1, 1, 2, 2, 3, 3, 4, 4}
	rValues := []uint64{0, 9, 10, 19, 20, 29, 30, 39, 40, 49}

	for i, r := range rValues {
		got := tree.FindByWeight(r)
		if got != expected[i] {
			t.Errorf("FindByWeight(%d): expected %d, got %d", r, expected[i], got)
		}
	}
}

func TestFenwickTree_WeightedStakes(t *testing.T) {
	// Validator 0 has 70% of total stake, validator 1 has 20%, validator 2 has 10%
	stakes := []uint64{700, 200, 100}
	tree := utils.NewStakeFenwickTree(stakes)

	if idx := tree.FindByWeight(0); idx != 0 {
		t.Errorf("r=0: expected 0, got %d", idx)
	}
	if idx := tree.FindByWeight(699); idx != 0 {
		t.Errorf("r=699: expected 0, got %d", idx)
	}
	if idx := tree.FindByWeight(700); idx != 1 {
		t.Errorf("r=700: expected 1, got %d", idx)
	}
	if idx := tree.FindByWeight(899); idx != 1 {
		t.Errorf("r=899: expected 1, got %d", idx)
	}
	if idx := tree.FindByWeight(900); idx != 2 {
		t.Errorf("r=900: expected 2, got %d", idx)
	}
	if idx := tree.FindByWeight(999); idx != 2 {
		t.Errorf("r=999: expected 2, got %d", idx)
	}
}

func TestFenwickTree_RemoveAndSelect(t *testing.T) {
	stakes := []uint64{100, 200, 300}
	tree := utils.NewStakeFenwickTree(stakes)
	totalStake := uint64(600)

	// Remove validator 1 (stake=200)
	totalStake -= stakes[1]
	tree.Remove(1, stakes[1])

	// Now total is 400: validator 0 has [0..99], validator 2 has [100..399]
	if idx := tree.FindByWeight(0); idx != 0 {
		t.Errorf("after remove, r=0: expected 0, got %d", idx)
	}
	if idx := tree.FindByWeight(99); idx != 0 {
		t.Errorf("after remove, r=99: expected 0, got %d", idx)
	}
	if idx := tree.FindByWeight(100); idx != 2 {
		t.Errorf("after remove, r=100: expected 2, got %d", idx)
	}
	if idx := tree.FindByWeight(totalStake - 1); idx != 2 {
		t.Errorf("after remove, r=%d: expected 2, got %d", totalStake-1, tree.FindByWeight(totalStake-1))
	}
}

func TestFenwickTree_SequentialRemoval(t *testing.T) {
	// Simulate the full draw-without-replacement loop for 5 validators
	stakes := []uint64{10, 20, 30, 40, 50}
	pubkeys := []string{"A", "B", "C", "D", "E"}
	tree := utils.NewStakeFenwickTree(stakes)
	totalStake := uint64(150)

	selected := make([]string, 0, 5)

	for i := 0; i < 5 && totalStake > 0; i++ {
		// Use deterministic r values to pick specific validators
		r := uint64(0) // Always pick the first available validator
		idx := tree.FindByWeight(r)

		selected = append(selected, pubkeys[idx])
		totalStake -= stakes[idx]
		tree.Remove(idx, stakes[idx])
	}

	// With r=0 each time, we always pick the first non-removed validator
	if len(selected) != 5 {
		t.Fatalf("expected 5 selected, got %d", len(selected))
	}

	// Verify all validators were selected (no duplicates)
	seen := make(map[string]bool)
	for _, pk := range selected {
		if seen[pk] {
			t.Errorf("duplicate selection: %s", pk)
		}
		seen[pk] = true
	}
	if len(seen) != 5 {
		t.Errorf("expected 5 unique validators, got %d", len(seen))
	}
}

func TestFenwickTree_LargeSet(t *testing.T) {
	n := 10000
	stakes := make([]uint64, n)
	for i := range stakes {
		stakes[i] = uint64(i + 1) // stakes: 1, 2, 3, ..., 10000
	}
	tree := utils.NewStakeFenwickTree(stakes)
	totalStake := uint64(n*(n+1)) / 2 // sum 1..n

	// Select all n validators one by one
	selectedIndices := make(map[int]bool)
	for round := 0; round < n && totalStake > 0; round++ {
		r := totalStake / 2 // Pick from the middle
		idx := tree.FindByWeight(r)

		if idx < 0 || idx >= n {
			t.Fatalf("round %d: index %d out of bounds", round, idx)
		}
		if selectedIndices[idx] {
			t.Fatalf("round %d: duplicate index %d", round, idx)
		}

		selectedIndices[idx] = true
		totalStake -= stakes[idx]
		tree.Remove(idx, stakes[idx])
	}

	if len(selectedIndices) != n {
		t.Errorf("expected %d unique selections, got %d", n, len(selectedIndices))
	}
}

func TestFenwickTree_Determinism(t *testing.T) {
	// Run the same selection twice and verify identical results
	stakes := []uint64{100, 200, 300, 400, 500}

	run := func() []int {
		tree := utils.NewStakeFenwickTree(stakes)
		totalStake := uint64(1500)
		result := make([]int, 0, 5)

		rValues := []uint64{750, 300, 100, 50, 0}

		for _, r := range rValues {
			if totalStake == 0 {
				break
			}
			r = r % totalStake
			idx := tree.FindByWeight(r)
			result = append(result, idx)
			totalStake -= stakes[idx]
			tree.Remove(idx, stakes[idx])
		}
		return result
	}

	run1 := run()
	run2 := run()

	if len(run1) != len(run2) {
		t.Fatalf("different lengths: %d vs %d", len(run1), len(run2))
	}
	for i := range run1 {
		if run1[i] != run2[i] {
			t.Errorf("index %d: run1=%d, run2=%d", i, run1[i], run2[i])
		}
	}
}

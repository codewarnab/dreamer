package theatricaltest

import (
	"testing"

	"assert"
)

// Good test: has direct assertions on t.
func TestGoodDirectAssertion(t *testing.T) {
	got := 1 + 1
	if got != 2 {
		t.Errorf("1+1 = %d, want 2", got)
	}
}

// Good test: has fatal assertion.
func TestGoodFatalAssertion(t *testing.T) {
	err := doSomething()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Good test: checks actual return values with assert.Equal.
func TestGoodValueAssertion(t *testing.T) {
	result := compute(5)
	assert.Equal(t, 25, result)
}

// Good test: checks non-structural property with assert.NoError.
func TestGoodErrorAssertion(t *testing.T) {
	err := doSomething()
	assert.NoError(t, err)
}

// Benchmark excluded from checks — benchmarks shouldn't assert.
func BenchmarkCompute(b *testing.B) {
	for i := 0; i < b.N; i++ {
		compute(i)
	}
}

// testHelperNotATest is a helper, not a test function — should be ignored.
func testHelperNotATest(t *testing.T) {
	t.Helper()
}

func doSomething() error { return nil }
func compute(x int) int  { return x * x }

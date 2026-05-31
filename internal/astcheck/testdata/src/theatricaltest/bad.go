package theatricaltest

import (
	"testing"

	"assert"
)

// Check 1: No-assertion tests — test always passes.
func TestEmptyTest(t *testing.T) { // want "test TestEmptyTest has no assertions"
	// No assertions at all — always passes.
	x := 1 + 1
	_ = x
}

func TestOnlySideEffects(t *testing.T) { // want "test TestOnlySideEffects has no assertions"
	// Calls non-assertion methods but never asserts.
	t.Log("doing stuff")
	t.Log("more stuff")
}

// Check 2: Assertion-only-on-non-target — mock assertions without return value checks.
type mockDB struct{}

func (m *mockDB) GetUser(id int) string                                      { return "alice" }
func (m *mockDB) AssertExpectations(t *testing.T)                            {}
func (m *mockDB) AssertCalled(t *testing.T, method string)                   {}
func (m *mockDB) AssertNumberOfCalls(t *testing.T, method string, count int) {}

func TestMockOnlyAssertions(t *testing.T) { // want "test TestMockOnlyAssertions only asserts on non-T receivers"
	m := &mockDB{}
	_ = m.GetUser(1)
	m.AssertExpectations(t)
	m.AssertCalled(t, "GetUser")
	m.AssertNumberOfCalls(t, "GetUser", 1)
}

// Check 3: Structural-only tests — assert/require calls that only check len/nil/fields.
func TestStructuralOnlyAssertLen(t *testing.T) { // want "test TestStructuralOnlyAssertLen only checks structural properties"
	items := []int{1, 2, 3}
	assert.Len(t, items, 3)
	assert.NotEmpty(t, items)
}

// assert.True is a correctness check (not structural), so this test is NOT flagged.
func TestNotStructuralWithTrue(t *testing.T) {
	var err error
	assert.Nil(t, err)
	assert.True(t, true)
}

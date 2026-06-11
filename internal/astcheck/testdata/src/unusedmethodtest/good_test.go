package unusedmethodtest

import "testing"

// testOnlyHelper is called only from this test file.
// The analyzer must not flag it: the runner loads the test-augmented package
// variant (dedupTestPackages), so the call below counts as a real caller.
func testOnlyHelper() int { return 42 }

func TestCallerUsingTestOnlyHelper(t *testing.T) {
	_ = testOnlyHelper()
}

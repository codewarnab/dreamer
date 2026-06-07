package unusedmethodtest

// runner is a package interface; closers are "used" through it.
type runner interface {
	run() error
}

type job struct{}

// run satisfies the runner interface — not flagged despite no direct calls.
func (job) run() error { return nil }

// Exported funcs are out of scope: external packages may call them.
func ExportedHelper() {}

// usedHelper is called below.
func usedHelper() int { return 2 }

// Note: "called only from a _test.go file" counts as used because the runner
// loads the test-augmented package variant (see dedupTestPackages); that case
// cannot be expressed in analysistest, which checks the non-test variant too.

func caller() int {
	var r runner = job{}
	_ = r.run()
	return usedHelper()
}

var _ = caller

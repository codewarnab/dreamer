package unusedmethodtest

// deadHelper has no callers anywhere in the package.
func deadHelper() int { // want "deadHelper is never used in this package"
	return 1
}

type widget struct{}

// deadMethod has no callers and satisfies no package interface.
func (widget) deadMethod() {} // want "deadMethod is never used in this package"

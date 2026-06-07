package nilcleanuptest

import "errors"

// good returns are silent — should produce zero diagnostics.

func goodStubWithErr(fail bool) (func(), error) {
	if fail {
		return func() {}, errors.New("setup failed")
	}
	return func() {}, nil
}

func goodNilNil() (func(), error) {
	// nil, nil is a different (success) contract — not flagged.
	return nil, nil
}

func goodNamedAssigned() (cleanup func(), err error) {
	cleanup = func() {}
	err = errors.New("boom")
	return
}

func goodNoFuncResult() (int, error) {
	return 0, errors.New("unrelated signature")
}

func goodNestedLitNotConfused() (func(), error) {
	helper := func() error {
		return errors.New("inner return is the closure's, not ours")
	}
	return func() { _ = helper() }, nil
}

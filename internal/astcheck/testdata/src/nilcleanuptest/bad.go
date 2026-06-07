package nilcleanuptest

import "errors"

func badNilWithErr(fail bool) (func(), error) {
	if fail {
		return nil, errors.New("setup failed") // want "nil cleanup func returned with error"
	}
	return func() {}, nil
}

func badNamedNeverAssigned() (cleanup func(), err error) {
	err = errors.New("boom")
	return // want "nil cleanup func returned with error"
}

func badErrVariable() (func(), error) {
	err := errors.New("x")
	return nil, err // want "nil cleanup func returned with error"
}

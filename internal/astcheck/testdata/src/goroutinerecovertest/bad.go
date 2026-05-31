package goroutinerecovertest

type runner struct{}

func (runner) Run() {}

func badNoRecover(r runner) {
	go func() { // want "goroutine runs the pipeline without a recover\\(\\) guard"
		r.Run()
	}()
}

func badRecoverNotFirst(r runner) {
	go func() { // want "goroutine runs the pipeline without a recover\\(\\) guard"
		r.Run()
		defer func() { recover() }()
	}()
}

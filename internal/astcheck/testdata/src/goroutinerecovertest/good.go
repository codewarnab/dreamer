package goroutinerecovertest

func goodWithRecover(r runner) {
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				_ = rec
			}
		}()
		r.Run()
	}()
}

// Goroutine that does not run the pipeline — not flagged even without recover.
func goodNoPipeline() {
	go func() {
		_ = 1 + 1
	}()
}

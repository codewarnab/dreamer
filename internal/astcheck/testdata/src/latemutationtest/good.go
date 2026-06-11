package latemutationtest

func use(cfg sessionConfig) {}

// Write before the pass: the callee sees it. Fine.
func writeThenPass() {
	cfg := sessionConfig{}
	cfg.Model = "early"
	use(cfg)
}

// Write after a pass but the variable is passed again: write is observed.
func writeThenRepass() {
	cfg := sessionConfig{}
	use(cfg)
	cfg.Model = "second"
	use(cfg)
}

// Write after a pass but the variable is returned: write is observed.
func writeThenReturn() sessionConfig {
	cfg := sessionConfig{}
	use(cfg)
	cfg.Model = "returned"
	return cfg
}

// Write inside a loop: lexical order does not imply execution order — the
// next iteration's use(cfg) sees the write.
func writeInLoop() {
	cfg := sessionConfig{}
	for i := 0; i < 3; i++ {
		use(cfg)
		cfg.Model = "next"
	}
}

// Address taken: writes may be observed through the pointer. Skip the var.
func writeAfterAlias() {
	cfg := sessionConfig{}
	p := &cfg
	use(cfg)
	cfg.Model = "aliased"
	_ = p
}

// Write inside a closure: execution timing is unknown; stay silent.
func writeInClosure() func() {
	cfg := sessionConfig{}
	use(cfg)
	return func() {
		cfg.Model = "deferred"
		use(cfg)
	}
}

package latemutationtest

type sessionConfig struct {
	Model string
	Hook  func(uintptr)
}

func newSession(cfg sessionConfig) {}

// The runner.go bug shape: hook assigned after the config was already
// consumed by value; nothing reads cfg afterward, so the write is lost.
func lostHookWrite() {
	cfg := sessionConfig{Model: "m"}
	newSession(cfg)
	cfg.Hook = func(uintptr) {} // want `field write to "cfg" after it was passed by value`
}

// Same shape with a plain field instead of a func field.
func lostPlainWrite() {
	cfg := sessionConfig{}
	newSession(cfg)
	cfg.Model = "late" // want `field write to "cfg" after it was passed by value`
}

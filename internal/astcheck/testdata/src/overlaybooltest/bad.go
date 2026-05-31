package overlaybooltest

type badOverlay struct {
	Enabled bool `overlay:"merge"` // want "overlay-merged field is bool; use \\*bool"
	Verbose bool `overlay:"merge" yaml:"verbose"` // want "overlay-merged field is bool; use \\*bool"
}

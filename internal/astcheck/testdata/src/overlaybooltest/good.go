package overlaybooltest

type goodOverlay struct {
	// Correctly modeled as a pointer so unset != explicit false.
	Enabled *bool `overlay:"merge"`
	// A plain bool without the overlay:"merge" tag is fine — not merged.
	Internal bool
	// Non-bool overlay fields are unaffected.
	Name string `overlay:"merge"`
}

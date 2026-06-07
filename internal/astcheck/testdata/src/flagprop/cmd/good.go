package cmd

import "fmt"

// good params are read or forwarded — should produce zero diagnostics.

func runCondition(verbose bool) {
	if verbose {
		fmt.Println("verbose on")
	}
}

func runForwarded(target string, dryRun bool) error {
	return printResult(target, dryRun)
}

func printResult(target string, dryRun bool) error {
	fmt.Println(target, dryRun)
	return nil
}

// Blank param is an explicit "intentionally unused" signal.
func runBlank(_ bool) {}

// Non-bool params are out of scope.
func runString(mode string) {
	_ = mode
}

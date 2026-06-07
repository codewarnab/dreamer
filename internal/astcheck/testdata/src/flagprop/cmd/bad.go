package cmd

import "fmt"

// runApply accepts dryRun but never reads it — output always claims a real run.
func runApply(target string, dryRun bool) error { // want "bool parameter dryRun is never read in runApply"
	fmt.Println("applying", target)
	return nil
}

package backgroundjobs

// runArgsFor returns the argument list for invoking "dreamer jobs run <jobID> --config <configPath> --run-token-file <tokenPath>".
// Shared across all platform schedulers.
func runArgsFor(jobID, configPath, storeDir string) []string {
	return []string{"jobs", "run", jobID, "--config", configPath, "--run-token-file", RunTokenPath(storeDir)}
}

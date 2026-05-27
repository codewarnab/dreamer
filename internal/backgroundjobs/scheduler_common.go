package backgroundjobs

// runArgsFor returns the argument list for invoking "dreamer jobs run <jobID> --config <configPath>".
// Shared across all platform schedulers.
func runArgsFor(jobID, configPath string) []string {
	return []string{"jobs", "run", jobID, "--config", configPath}
}

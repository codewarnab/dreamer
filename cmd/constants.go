package cmd

import "time"

// Lockfile interaction timeouts and poll intervals.
const (
	// lockfileWaitTimeout is how long start/stop poll for lockfile changes.
	// Short for start (daemon writes lock early); longer for stop (signal
	// handler needs time to clean up).
	lockfileWaitTimeoutShort = 2 * time.Second
	lockfileWaitTimeoutLong  = 5 * time.Second

	// lockfilePollInterval is how often start/stop check the lockfile.
	lockfilePollIntervalFast = 100 * time.Millisecond
	lockfilePollIntervalSlow = 200 * time.Millisecond
)

// Web server and health probe.
const (
	// webShutdownTimeout gives in-flight HTTP requests time to finish
	// during graceful shutdown.
	webShutdownTimeout = 5 * time.Second

	// healthProbeTimeout is the TCP dial deadline when checking if the
	// daemon's web server is reachable.
	healthProbeTimeout = 250 * time.Millisecond

	// activityRingSize is the number of recent events kept in the
	// dashboard's live-activity ring buffer.
	activityRingSize = 20

	// workerShutdownGrace bounds how long daemon shutdown waits for
	// in-flight analysis jobs to drain. The signal handler has already
	// canceled the run context by the time Stop() is called, so workers
	// are winding down; this is only an upper bound on how long we wait
	// before abandoning a stuck provider subprocess. Without it, Ctrl+C
	// could block for the full max_analysis_duration (default 8h).
	workerShutdownGrace = 10 * time.Second
)

// Status command.
const (
	// recentFilterWindow is the default time window for "recent" jobs
	// when --all is not set.
	recentFilterWindow = 24 * time.Hour
)

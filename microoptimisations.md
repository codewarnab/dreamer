# Code Micro-optimizations

## Folder: `cmd`

### 1. Slice Pre-allocation in `status.go`

In [cmd/status.go](file:///c:/Users/User/code/dreamer/cmd/status.go):
- **Lines 66-72 (`filterStatus`)**:
  ```go
  var filtered []*jobqueue.Job
  for _, job := range status.Jobs {
      if job.Project == project {
          filtered = append(filtered, job)
      }
  }
  ```
  *Optimization*: Preallocate the slice capacity to reduce slice growth and allocations:
  ```go
  filtered := make([]*jobqueue.Job, 0, len(status.Jobs))
  ```

- **Lines 81-89 (`filterRecent`)**:
  ```go
  var filtered []*jobqueue.Job
  ```
  *Optimization*: Same as above, preallocate capacity:
  ```go
  filtered := make([]*jobqueue.Job, 0, len(status.Jobs))
  ```

- **Lines 171-175 (`printStatusTable`)**:
  ```go
  finishedJobs := make([]*jobqueue.Job, 0)
  finishedJobs = append(finishedJobs, groups[jobqueue.StatusCompleted]...)
  finishedJobs = append(finishedJobs, groups[jobqueue.StatusFailed]...)
  finishedJobs = append(finishedJobs, groups[jobqueue.StatusTimedOut]...)
  finishedJobs = append(finishedJobs, groups[jobqueue.StatusCancelled]...)
  ```
  *Optimization*: Preallocate the slice capacity to avoid multiple allocations as we append slices:
  ```go
  cap := len(groups[jobqueue.StatusCompleted]) + len(groups[jobqueue.StatusFailed]) +
         len(groups[jobqueue.StatusTimedOut]) + len(groups[jobqueue.StatusCancelled])
  finishedJobs := make([]*jobqueue.Job, 0, cap)
  ```

## Folder: `internal/analyzer`

### 1. Slice Pre-allocation in `promptbuilder.go`

In [internal/analyzer/promptbuilder.go](file:///c:/Users/User/code/dreamer/internal/analyzer/promptbuilder.go):
- **Lines 295-301 (`orderedByCategory`)**:
  ```go
  func orderedByCategory[T any](in map[RuleCategory][]T, order []RuleCategory) []T {
  	out := []T{}
  	for _, c := range order {
  		out = append(out, in[c]...)
  	}
  	return out
  }
  ```
  *Optimization*: Calculate the total length of all slices in the map before allocating the slice to avoid repeated growth allocations:
  ```go
  func orderedByCategory[T any](in map[RuleCategory][]T, order []RuleCategory) []T {
  	var total int
  	for _, c := range order {
  		total += len(in[c])
  	}
  	out := make([]T, 0, total)
  	for _, c := range order {
  		out = append(out, in[c]...)
  	}
  	return out
  }
  ```

### 2. Lowercase Allocations in `orchestrator.go`

In [internal/analyzer/orchestrator.go](file:///c:/Users/User/code/dreamer/internal/analyzer/orchestrator.go):
  *Optimization*: Calling `strings.ToLower` creates new string allocations. If this function is run in a critical loop, string allocations can be avoided by writing a custom lowercase writer, or writing byte-by-byte converting characters directly to lowercase on the fly.

## Folder: `internal/astcheck`

### 1. Slice Pre-allocation in `report.go`

In [internal/astcheck/report.go](file:///c:/Users/User/code/dreamer/internal/astcheck/report.go):
- **Lines 55-61 (`WriteText`)**:
  ```go
  grouped := make(map[string][]Finding)
  fileOrder := make([]string, 0)
  ```
  *Optimization*: Preallocate the `fileOrder` capacity since it cannot exceed `len(findings)`:
  ```go
  fileOrder := make([]string, 0, len(findings))
  ```

### 2. Slice Pre-allocation in `baseline.go`

In [internal/astcheck/baseline.go](file:///c:/Users/User/code/dreamer/internal/astcheck/baseline.go):
- **Lines 96-107 (`SubtractBaseline`)**:
  ```go
  var out []Finding
  for _, f := range findings {
      if !baseline[f.Key()] {
          out = append(out, f)
      }
  }
  ```
  *Optimization*: Preallocate the slice `out` to match capacity of input `findings` to avoid dynamic growth overhead:
  ```go
  out := make([]Finding, 0, len(findings))
  ```

## Folder: `internal/backgroundjobs`

### 1. Slice Pre-allocation in `health.go`

In [internal/backgroundjobs/health.go](file:///c:/Users/User/code/dreamer/internal/backgroundjobs/health.go):
- **Lines 64-68 (`CheckHealth`)**:
  ```go
	health := SystemHealth{
		TotalJobs: len(state.Jobs),
		Issues:    []HealthIssue{},
		JobHealth: []JobHealth{},
	}
  ```
  *Optimization*: Preallocate the capacity of `JobHealth` slice to match the exact size of `state.Jobs` to avoid allocations during loop appends:
  ```go
	health := SystemHealth{
		TotalJobs: len(state.Jobs),
		Issues:    []HealthIssue{},
		JobHealth: make([]JobHealth, 0, len(state.Jobs)),
	}
  ```

## Folder: `internal/categories`

### 1. Slice Allocation Reuse in `categories.go`

In [internal/categories/categories.go](file:///c:/Users/User/code/dreamer/internal/categories/categories.go):
- **Lines 19-28 (`All`)**:
  ```go
  func All() []Category {
  	return []Category{
  		LintRule,
  		Test,
  		CICheck,
  		Doc,
  		Config,
  		RefactorBoundary,
  	}
  }
  ```
  *Optimization*: Calling `All()` constructs and allocates a new slice on the heap every single time. Instead, define a global slice and return it, or return a static array if mutability is a concern:
  ```go
  var allCategories = []Category{
  	LintRule,
  	Test,
  	CICheck,
  	Doc,
  	Config,
  	RefactorBoundary,
  }

  func All() []Category {
  	return allCategories
  }
  ```

## Folder: `internal/chat`

### 1. Slice Pre-allocation in `discovery.go`

In [internal/chat/discovery.go](file:///c:/Users/User/code/dreamer/internal/chat/discovery.go):
- **Lines 91-94 (`discoverChatsFromEnvironment`)**:
  ```go
  combined := make([]Source, 0)
  for _, sources := range results {
  	combined = append(combined, sources...)
  }
  ```
  *Optimization*: Calculate the total size of all sources returned by the providers and preallocate `combined`'s capacity to avoid slice growth overhead:
  ```go
  var totalSources int
  for _, sources := range results {
  	totalSources += len(sources)
  }
  combined := make([]Source, 0, totalSources)
  for _, sources := range results {
  	combined = append(combined, sources...)
  }
  ```

## Folder: `internal/config`

### 1. Allocation Avoidance in `yaml_ops.go`

In [internal/config/yaml_ops.go](file:///c:/Users/User/code/dreamer/internal/config/yaml_ops.go):
- **Lines 80-81 (`RemoveProjectFromYAML`) & Lines 92-97 (`strBuilderWriter`)**:
  ```go
	var buf strings.Builder
	enc := yaml.NewEncoder(&strBuilderWriter{b: &buf})
  ```
  *Optimization*: `strings.Builder` natively satisfies the `io.Writer` interface in Go (it defines `Write(p []byte) (int, error)`). The custom wrapper type `strBuilderWriter` is unnecessary and causes an extra allocation. We can pass `&buf` directly:
  ```go
	  var buf strings.Builder
  enc := yaml.NewEncoder(&buf)
  ```
  This permits deleting the `strBuilderWriter` helper struct entirely.

## Folder: `internal/errs`

### 1. Lazy Map Allocation in `errs.go`

In [internal/errs/errs.go](file:///c:/Users/User/code/dreamer/internal/errs/errs.go):
- **Lines 56-59 (`newErr`) & Lines 113-119 (`ProviderUnavailable`)**:
  ```go
  func newErr(kind Kind, provider, op, message, hint string, details map[string]any, cause error) *Error {
  	if details == nil {
  		details = map[string]any{}
  	}
  ```
  and
  ```go
  func ProviderUnavailable(provider, op string, cause error) *Error {
  	return newErr(KindProviderUnavailable, provider, op,
  		provider+" provider unavailable",
  		"",
  		map[string]any{},
  		cause,
  	)
  }
  ```
  *Optimization*: Allocating empty maps like `map[string]any{}` introduces heap allocations for every error created, even if they have no extra details. Instead, keep `details` as `nil` when it is empty or omitted, and only check for `nil` when reading or merging. Change `ProviderUnavailable` to pass `nil` and change `newErr` to avoid allocating an empty map if `details == nil`:
  ```go
  func newErr(kind Kind, provider, op, message, hint string, details map[string]any, cause error) *Error {
  	// Keep details nil if there are none, avoiding map allocation.
  ```

## Folder: `internal/fsutil`

### 1. Prepend Allocation Overhead in `path.go`

In [internal/fsutil/path.go](file:///c:/Users/User/code/dreamer/internal/fsutil/path.go):
- **Lines 92-93 (`ResolveSymlinks`)**:
  ```go
  base := filepath.Base(currentPath)
  tail = append([]string{base}, tail...)
  ```
  *Optimization*: Prepending to a slice using `append([]string{item}, slice...)` creates a new slice allocation on every iteration. Instead, append to the slice normally (`tail = append(tail, base)`) and iterate in reverse order when building the path to avoid allocations:
  ```go
  tail = append(tail, base)
  ...
  for i := len(tail) - 1; i >= 0; i-- {
      out = filepath.Join(out, tail[i])
  }
  ```

## Folder: `internal/jobqueue`

### 1. Map Lookup Overhead in `job.go`

In [internal/jobqueue/job.go](file:///c:/Users/User/code/dreamer/internal/jobqueue/job.go):
- **Lines 23-33 (`IsTerminal`)**:
  ```go
  var terminalStatuses = map[Status]bool{
  	StatusCompleted: true,
  	StatusFailed:    true,
  	StatusTimedOut:  true,
  	StatusCancelled: true,
  }

  func (s Status) IsTerminal() bool {
  	return terminalStatuses[s]
  }
  ```
  *Optimization*: Static map lookups are slower than switch statements, requiring hash computation and map lookup overhead. We can optimize this by replacing the map with a simple, compiler-optimized switch statement:
  ```go
  func (s Status) IsTerminal() bool {
  	switch s {
  	case StatusCompleted, StatusFailed, StatusTimedOut, StatusCancelled:
  		return true
  	default:
  		return false
  	}
  }
  ```

## Folder: `internal/logging`

### 1. Avoid Slice Allocations for Zero-Attribute Logs in `logger.go`

In [internal/logging/logger.go](file:///c:/Users/User/code/dreamer/internal/logging/logger.go):
- **Lines 169-174 (`write`)**:
  ```go
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		args = append(args, attr)
	}
	l.handler.Log(context.Background(), level, message, args...)
  ```
  *Optimization*: This code converts slog's `Attr` objects to `any` by allocating a new slice every time a log is written. When a log is written without any fields (`len(attrs) == 0`), we still make an allocation. We can short-circuit the zero-attribute case to avoid slice allocations entirely, and pre-allocate the exact size slice when attributes are present:
  ```go
	if len(attrs) == 0 {
		l.handler.Log(context.Background(), level, message)
		return
	}
	args := make([]any, len(attrs))
	for i, attr := range attrs {
		args[i] = attr
	}
	l.handler.Log(context.Background(), level, message, args...)
  ```

## Folder: `internal/mcpserver`

### 1. Slice Append Allocation in `validation.go`

In [internal/mcpserver/validation.go](file:///c:/Users/User/code/dreamer/internal/mcpserver/validation.go):
- **Lines 291-297 (`Record`)**:
  ```go
	line, err := json.Marshal(f)
	if err != nil {
		return 0, fmt.Errorf("marshal finding: %w", err)
	}
	if _, err := r.file.Write(append(line, '\n')); err != nil {
		return 0, fmt.Errorf("write finding: %w", err)
	}
  ```
  *Optimization*: Appending the newline character `'\n'` to `line` forces Go to allocate a new backing slice and copy all marshalled bytes into it. Since this writes to an unbuffered file, we can either use a buffered writer (`bufio.Writer`) or write `line` and `'\n'` sequentially to eliminate this slice allocation:
  ```go
	if _, err := r.file.Write(line); err != nil {
		return 0, fmt.Errorf("write finding line: %w", err)
	}
	if _, err := r.file.Write([]byte{'\n'}); err != nil {
		return 0, fmt.Errorf("write finding newline: %w", err)
	}
  ```





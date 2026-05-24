package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"dreamer/internal/mcpserver"
	"github.com/spf13/cobra"
)

const maxStdinBytes = 1 << 20 // 1 MiB — a single finding is never larger

func newRecordFindingCommand() *cobra.Command {
	var outputPath string

	command := &cobra.Command{
		Use:   "record-finding",
		Short: "Record a finding from stdin JSON (Phase 2 CLI tool).",
		Long: "Reads a JSON finding from stdin and appends it to a JSONL file. " +
			"Used by gemini-cli Phase 2 via Bash tool: echo '<json>' | dreamer record-finding --output <path>",
		Hidden:       true, // internal use only — called by the model via Bash
		SilenceUsage: true, // don't print usage on error — model reads stdout JSON
		RunE: func(_ *cobra.Command, _ []string) error {
			return runRecordFinding(outputPath, os.Stdin, os.Stdout)
		},
	}

	command.Flags().StringVar(&outputPath, "output", "", "Path to append findings as JSONL (required)")
	if err := command.MarkFlagRequired("output"); err != nil {
		// MarkFlagRequired only fails when the flag does not exist —
		// impossible here since we just registered it. Panic so a future
		// rename surfaces immediately rather than degrading silently.
		panic(fmt.Sprintf("mark --output required: %v", err))
	}
	return command
}

// runRecordFinding is the testable core of the record-finding subcommand.
// stdin/stdout are injected so unit tests can drive it without manipulating
// the process file descriptors.
//
// Exit-code contract:
//   - The model parses the stdout JSON regardless of exit code, so model-
//     correctable problems (bad JSON, validation failures, oversized input)
//     return (RecordResult{OK:false}, nil) — the cobra layer sees no error
//     and exits 0.
//   - Protocol / environment failures the model cannot fix (recorder open
//     fails, marshal-result fails, stdin read fails) return a non-nil error
//     so cobra exits non-zero — operators see real bugs instead of silent
//     "exit 0 with ok:false" sequences.
func runRecordFinding(outputPath string, stdin io.Reader, stdout io.Writer) error {
	input, err := io.ReadAll(io.LimitReader(stdin, maxStdinBytes+1))
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if len(input) == 0 {
		return writeRecordResult(stdout, mcpserver.RecordResult{OK: false, Error: "no input on stdin"})
	}
	if len(input) > maxStdinBytes {
		return writeRecordResult(stdout, mcpserver.RecordResult{OK: false, Error: "input too large (max 1 MiB)"})
	}

	var finding mcpserver.FindingInput
	if err := json.Unmarshal(input, &finding); err != nil {
		return writeRecordResult(stdout, mcpserver.RecordResult{OK: false, Error: fmt.Sprintf("invalid JSON: %v", err)})
	}

	recorder, err := mcpserver.NewFindingRecorder(outputPath)
	if err != nil {
		return fmt.Errorf("open recorder: %w", err)
	}
	defer recorder.Close()

	total, err := recorder.Record(&finding)
	if err != nil {
		if mcpserver.IsValidationError(err) {
			return writeRecordResult(stdout, mcpserver.RecordResult{OK: false, Error: err.Error()})
		}
		return fmt.Errorf("record finding: %w", err)
	}
	return writeRecordResult(stdout, mcpserver.RecordResult{OK: true, Total: total})
}

// writeRecordResult marshals and writes the result. Marshal failure is a
// hard error (the result is a fixed struct, so it cannot fail in practice
// — failure means the runtime is broken, not a model problem).
func writeRecordResult(w io.Writer, r mcpserver.RecordResult) error {
	encoded, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	if _, err := fmt.Fprintln(w, string(encoded)); err != nil {
		return fmt.Errorf("write result: %w", err)
	}
	return nil
}

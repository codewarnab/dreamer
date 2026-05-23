package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"dreamer/internal/mcpserver"
	"github.com/spf13/cobra"
)

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
			if outputPath == "" {
				return fmt.Errorf("--output is required")
			}

			input, err := io.ReadAll(os.Stdin)
			if err != nil {
				printRecordResult(mcpserver.RecordResult{OK: false, Error: fmt.Sprintf("read stdin: %v", err)})
				return nil
			}
			if len(input) == 0 {
				printRecordResult(mcpserver.RecordResult{OK: false, Error: "no input on stdin"})
				return nil
			}

			var finding mcpserver.FindingInput
			if err := json.Unmarshal(input, &finding); err != nil {
				printRecordResult(mcpserver.RecordResult{OK: false, Error: fmt.Sprintf("invalid JSON: %v", err)})
				return nil
			}

			recorder, err := mcpserver.NewFindingRecorder(outputPath)
			if err != nil {
				printRecordResult(mcpserver.RecordResult{OK: false, Error: err.Error()})
				return nil
			}
			defer recorder.Close()

			total, err := recorder.Record(&finding)
			if err != nil {
				printRecordResult(mcpserver.RecordResult{OK: false, Error: err.Error()})
				return nil
			}

			printRecordResult(mcpserver.RecordResult{OK: true, Total: total})
			return nil
		},
	}

	command.Flags().StringVar(&outputPath, "output", "", "Path to append findings as JSONL (required)")
	_ = command.MarkFlagRequired("output")
	return command
}

// printRecordResult writes the JSON result to stdout for the model to read.
func printRecordResult(r mcpserver.RecordResult) {
	b, err := json.Marshal(r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal result: %v\n", err)
		return
	}
	fmt.Println(string(b))
}

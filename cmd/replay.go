package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"dreamer/internal/capture"
	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
	"dreamer/internal/replay"
)

// newReplayCommand returns "dreamer replay <project> <run-id> --call N".
// Default mode re-parses a captured response (free). --resend sends the
// stored prompt back to a provider, optionally overriding provider/model.
func newReplayCommand() *cobra.Command {
	var (
		callIndex int
		resend    bool
		provider  string
		model     string
		jsonOut   bool
	)

	command := &cobra.Command{
		Use:   "replay <project> <run-id> --call <index>",
		Short: "Re-run one captured LLM call: re-parse its response or re-send it.",
		Long: "Re-parse decodes a captured raw response again with the current rule packs — free, no LLM call. " +
			"Use it to recover mistakes that were dropped because a response failed to parse. " +
			"--resend sends the exact stored prompt back through the provider, so you can compare " +
			"models on identical input; pass --provider/--model to override the captured session parameters. " +
			"Resend results are captured under a new run tagged as a replay of the parent.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			projectName, runID := args[0], args[1]

			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			outputRoot, appConfig, err := resolveOutputRoot(cmd, resolvedConfigPath)
			if err != nil {
				return err
			}
			if err := validateConfiguredProject(appConfig, projectName); err != nil {
				return err
			}
			projectPath := projectPathFor(appConfig, projectName)

			packs, err := pipeline.BuildRulePacks(appConfig, projectPath)
			if err != nil {
				return fmt.Errorf("load rule packs: %w", err)
			}

			ctx := commandContext(cmd)
			if !resend {
				result, err := replay.Reparse(outputRoot, projectName, runID, callIndex, packs)
				if err != nil {
					return err
				}
				if meta, _, loadErr := capture.LoadRun(capture.RunsRoot(outputRoot, projectName), runID); loadErr == nil {
					result.ProviderID = meta.ProviderID
					result.Model = meta.Model
				}
				return printReplayResult(cmd, result, jsonOut)
			}

			logger := logging.Silent()
			result, err := replay.Resend(ctx, replay.Options{
				OutputRoot:       outputRoot,
				ProjectName:      projectName,
				RunID:            runID,
				CallIndex:        callIndex,
				ProviderOverride: provider,
				ModelOverride:    model,
				AppConfig:        appConfig,
				Packs:            packs,
				Logger:           logger,
			})
			if err != nil {
				return err
			}
			return printReplayResult(cmd, result, jsonOut)
		},
	}

	command.Flags().IntVar(&callIndex, "call", -1, "Index of the captured call to replay (see: dreamer runs <project> --run <id>).")
	command.Flags().BoolVar(&resend, "resend", false, "Send the stored prompt to the provider again (default: re-parse only).")
	command.Flags().StringVarP(&provider, "provider", "P", "", "Override the provider for --resend (default: captured provider).")
	command.Flags().StringVar(&model, "model", "", "Override the model for --resend (default: captured model).")
	command.Flags().BoolVar(&jsonOut, "json", false, "Machine-readable JSON output.")
	command.Flags().String(outputRootFlag, "", "Override daemon.output_root from config.")
	return command
}

func printReplayResult(cmd *cobra.Command, result replay.Result, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	mode := result.Mode
	if mode == capture.ReplayModeReparse {
		cmd.Printf("Re-parsed %s call #%d in %dms\n", result.ParentRunID, result.CallIndex, result.DurationMS)
	} else {
		cmd.Printf("Re-sent %s call #%d via %s/%s in %ds (captured as %s)\n",
			result.ParentRunID, result.CallIndex, result.ProviderID, result.Model, result.DurationMS/1000, result.ReplayRunID)
	}
	if result.ParseError != "" {
		cmd.Printf("\nStill failing to parse:\n  %s\n", result.ParseError)
		return nil
	}
	if result.Summary != "" {
		cmd.Printf("\nSummary: %s\n", result.Summary)
	}
	if len(result.Mistakes) == 0 {
		cmd.Println("\nNo mistakes decoded from this response.")
		return nil
	}
	cmd.Printf("\n%d mistake(s) recovered:\n\n", len(result.Mistakes))
	for _, m := range result.Mistakes {
		confidencePct := int(m.Confidence*100 + 0.5)
		cmd.Printf("  [%s %3d%%] %s\n", m.Category, confidencePct, m.Summary)
		if m.EvidenceExcerpt != "" && m.EvidenceExcerpt != m.Summary {
			excerpt := m.EvidenceExcerpt
			if len(excerpt) > 120 {
				excerpt = excerpt[:117] + "..."
			}
			cmd.Printf("           evidence: %s\n", excerpt)
		}
	}
	for _, w := range result.Warnings {
		cmd.Printf("  warning: %s\n", w)
	}
	return nil
}

// projectPathFor returns the configured path of a validated project name.
func projectPathFor(appConfig *config.App, name string) string {
	for _, p := range appConfig.Projects {
		if p.Name == name {
			return p.Path
		}
	}
	return ""
}

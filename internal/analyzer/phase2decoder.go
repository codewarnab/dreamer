package analyzer

import (
	"fmt"
	"strings"

	"dreamer/internal/mcpserver"
)

// Phase 2 has three transports for getting findings back from the model:
// inline JSON (legacy), an MCP tool call, or a Bash-invoked CLI tool. Each
// transport has its own prompt tail (instructions to the model on how to
// emit findings) and its own decoder (how to turn the session's raw output
// into Findings).
//
// This file isolates the per-transport logic so the orchestrator and
// promptbuilder stay transport-agnostic. Adding a new transport means
// registering a new entry in phase2Decoders — no edits to orchestrator,
// promptbuilder, or pipeline.

// phase2Decoder bundles the two transport-specific operations.
type phase2Decoder struct {
	// tail returns the per-mode trailing instructions appended to the
	// Phase 2 prompt. lead is the first enabled rule pack (used to source
	// the response schema or recording instructions from YAML).
	tail func(req PhaseRequest, lead *RulePack) string
	// decode turns one Phase 2 session's raw text response plus the
	// findings output path into per-category Findings. raw is the
	// session's stdout; outputPath is the JSONL file the recording
	// transport wrote into (empty for the JSON transport). req carries
	// active rule packs, output path, and the optional finding redactor.
	decode func(raw string, req PhaseRequest, packs []RulePack) (map[RuleCategory][]Finding, []string, error)
}

// phase2Decoders is the transport registry. Adding a transport is a one-line
// addition here plus the implementations below.
var phase2Decoders = map[Phase2Mode]phase2Decoder{
	Phase2ModeNone: {tail: jsonPhase2Tail, decode: jsonPhase2Decode},
	Phase2ModeMCP:  {tail: mcpPhase2Tail, decode: filePhase2Decode},
	Phase2ModeCLI:  {tail: cliPhase2Tail, decode: filePhase2Decode},
}

// lookupPhase2Decoder returns the decoder for the given mode, falling back
// to the JSON decoder if the mode is unknown (defensive — should be
// unreachable because Phase2Mode constants are an enum).
func lookupPhase2Decoder(mode Phase2Mode) phase2Decoder {
	if d, ok := phase2Decoders[mode]; ok {
		return d
	}
	return phase2Decoders[Phase2ModeNone]
}

// buildPhase2Tail is the single entry point promptbuilder calls. It
// dispatches to the registered tail for the request's mode.
func buildPhase2Tail(req PhaseRequest, lead *RulePack) string {
	return lookupPhase2Decoder(req.Phase2Mode).tail(req, lead)
}

// jsonPhase2Tail produces the legacy JSON-output tail.
func jsonPhase2Tail(_ PhaseRequest, lead *RulePack) string {
	if lead == nil {
		return ""
	}
	return "Return JSON only with this exact shape:\n" + lead.EffectivePhase2ResponseSchema()
}

// mcpPhase2Tail produces the recording instructions for the MCP transport.
// The YAML rule pack supplies the literal text (so operators can tune it
// without recompiling); we fall back to JSON if the pack has no
// recording-instructions block.
func mcpPhase2Tail(_ PhaseRequest, lead *RulePack) string {
	if lead == nil {
		return ""
	}
	if instr := lead.EffectivePhase2RecordingInstructions(); instr != "" {
		return instr
	}
	return jsonPhase2Tail(PhaseRequest{}, lead)
}

// cliPhase2Tail produces the Bash-tool recording instructions for gemini-cli.
//
// Uses a heredoc (<<'ENDOFFINDING') to pipe the finding JSON via stdin
// instead of echo + single-quoting. A model-generated finding string
// containing a single quote, $(...), or a backtick would otherwise break out
// of `echo '...'` and execute arbitrary shell — gemini-cli's Bash tool is
// allowlisted in `default` approval mode, so a hostile transcript could
// achieve RCE in the project working directory.
//
// The binary path comes from the resolved request (not a hard-coded
// "dreamer") because the running binary may not be on $PATH — see
// internal/mcpserver.FindDreamerBinary.
func cliPhase2Tail(req PhaseRequest, _ *RulePack) string {
	binaryPath := req.CLIBinaryPath
	if binaryPath == "" {
		binaryPath = "dreamer"
	}
	return fmt.Sprintf(`Use the Bash tool to record each verified finding.

For each finding, run this exact pattern (copy the structure, fill in values):

cat <<'ENDOFFINDING' | %s record-finding --output %s
{"category":"<category-id>","mistake":"<one sentence>","guardrail":{"kind":"<category>","tool":"<tool-name>","rule":"<rule-name>"},"confidence":<0.0-1.0>}
ENDOFFINDING

The finding JSON format:
{
  "category": "<category-id>",
  "mistake": "<one sentence describing the mistake>",
  "guardrail": {"kind": "<category>", "tool": "<tool-name>", "rule": "<rule-name>"},
  "codebase_evidence": [{"path": "<relative-path>", "lines": "<range>", "symbol": "<name>"}],
  "confidence": <0.0-1.0>
}

Do NOT return findings as JSON text. Use the command for each finding individually.
If the command returns {"ok":false}, fix the input and retry.
After recording all findings, confirm completion with the count.`, binaryPath, req.FindingsOutputPath)
}

// jsonPhase2Decode parses inline JSON findings from the session's raw output.
func jsonPhase2Decode(raw string, req PhaseRequest, packs []RulePack) (map[RuleCategory][]Finding, []string, error) {
	findings, warnings, err := parsePhase2Response(raw, packs)
	if err != nil {
		return nil, warnings, err
	}
	applyFindingRedactor(findings, req.FindingRedactor)
	return findings, warnings, nil
}

// filePhase2Decode reads findings the recording transport wrote to a JSONL
// file (used by both MCP and CLI modes). raw is ignored — the truth is on
// disk. A missing or empty file means the model recorded zero findings,
// which is a legitimate outcome and returns nil, nil, nil.
//
// Read errors are surfaced as an error (caller decides whether to abort or
// fall back). Unknown/disabled categories produce warnings and are dropped,
// mirroring jsonPhase2Decode behavior.
func filePhase2Decode(_ string, req PhaseRequest, packs []RulePack) (map[RuleCategory][]Finding, []string, error) {
	if req.FindingsOutputPath == "" {
		return nil, nil, fmt.Errorf("file decoder: FindingsOutputPath is empty")
	}
	raw, err := mcpserver.ReadFindingsJSONL(req.FindingsOutputPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read phase-2 findings file %q: %w", req.FindingsOutputPath, err)
	}
	findings, warnings, err := materializeRecordedFindings(raw, packs)
	if err != nil {
		return nil, warnings, err
	}
	applyFindingRedactor(findings, req.FindingRedactor)
	return findings, warnings, nil
}

// applyFindingRedactor scrubs secrets out of every text-bearing field on
// every Finding. Mistake text, guardrail snippets, apply snippets, and
// evidence symbols/paths all flow through. Paths intentionally do not run
// through the redactor — they're filesystem paths, not transcript content,
// and redaction would only obscure them. Lines (line ranges) likewise
// passthrough.
func applyFindingRedactor(findings map[RuleCategory][]Finding, redact func(string) string) {
	if redact == nil {
		return
	}
	for cat, list := range findings {
		for i := range list {
			list[i].Mistake = redact(list[i].Mistake)
			list[i].Guardrail.Tool = redact(list[i].Guardrail.Tool)
			list[i].Guardrail.Rule = redact(list[i].Guardrail.Rule)
			list[i].Guardrail.ConfigSnippet = redact(list[i].Guardrail.ConfigSnippet)
			if list[i].Guardrail.Apply != nil {
				list[i].Guardrail.Apply.Snippet = redact(list[i].Guardrail.Apply.Snippet)
				list[i].Guardrail.Apply.Anchor = redact(list[i].Guardrail.Apply.Anchor)
			}
			for j := range list[i].Evidence {
				list[i].Evidence[j].Symbol = redact(list[i].Evidence[j].Symbol)
			}
		}
		findings[cat] = list
	}
}

// materializeRecordedFindings converts validated MCP/CLI FindingInput values
// into orchestrator Findings, dropping unknown/disabled categories with a
// warning (mirrors parsePhase2Response behavior). Intra-call duplicates are
// collapsed by finding hash.
func materializeRecordedFindings(raw []mcpserver.FindingInput, packs []RulePack) (map[RuleCategory][]Finding, []string, error) {
	categoryByID := categoryLookup(packs)
	findingsByCategory := map[RuleCategory][]Finding{}
	seenHashes := map[string]struct{}{}
	warnings := []string{}

	for _, f := range raw {
		catID := strings.TrimSpace(strings.ToLower(f.Category))
		pack, ok := categoryByID[catID]
		if !ok || !pack.Enabled {
			warnings = append(warnings, fmt.Sprintf("phase-2 recorded unknown/disabled category %q; dropped", f.Category))
			continue
		}
		mistake := strings.TrimSpace(f.Mistake)
		if mistake == "" {
			continue
		}

		guardrail := Guardrail{
			Kind:          f.Guardrail.Kind,
			Tool:          f.Guardrail.Tool,
			Rule:          f.Guardrail.Rule,
			ConfigSnippet: f.Guardrail.ConfigSnippet,
		}
		if f.Guardrail.Apply != nil {
			guardrail.Apply = &ApplySpec{
				TargetFile: f.Guardrail.Apply.TargetFile,
				Strategy:   f.Guardrail.Apply.Strategy,
				Anchor:     f.Guardrail.Apply.Anchor,
				Snippet:    f.Guardrail.Apply.Snippet,
			}
		}
		evidence := make([]CodebaseEvidence, 0, len(f.CodebaseEvidence))
		for _, e := range f.CodebaseEvidence {
			evidence = append(evidence, CodebaseEvidence{
				Path:   e.Path,
				Lines:  e.Lines,
				Symbol: e.Symbol,
			})
		}

		finding := buildFinding(pack.Category, mistake, f.Confidence, guardrail, evidence)
		if _, dup := seenHashes[finding.Hash]; dup {
			continue
		}
		seenHashes[finding.Hash] = struct{}{}
		findingsByCategory[pack.Category] = append(findingsByCategory[pack.Category], finding)
	}
	return findingsByCategory, warnings, nil
}

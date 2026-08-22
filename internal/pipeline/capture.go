package pipeline

import (
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/capture"
	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/state"
)

// callRecorder adapts analyzer.CapturedCall events onto a capture.Writer.
// It applies response redaction (a model may echo transcript secrets back)
// and never fails the analysis: append errors are logged, not returned.
type callRecorder struct {
	w        *capture.Writer
	redactor *analyzer.Redactor
	logger   *logging.Logger
}

// CaptureCall implements analyzer.CallCapture.
func (r *callRecorder) CaptureCall(call analyzer.CapturedCall) {
	rec := capture.Record{
		Phase:      call.Phase,
		ChunkIndex: call.ChunkIndex,
		ChunkCount: call.ChunkCount,
		Prompt:     call.Prompt,
		Response:   call.Response,
		DurationMS: call.Elapsed.Milliseconds(),
		Timestamp:  time.Now().UTC(),
	}
	switch {
	case call.RunError != nil:
		rec.Status = capture.StatusError
		rec.Error = state.TruncateError(call.RunError.Error())
	case call.ParseError != nil:
		rec.Status = capture.StatusParseFailed
		rec.Error = state.TruncateError(call.ParseError.Error())
	default:
		rec.Status = capture.StatusOK
	}
	if rec.Response != "" && r.redactor != nil {
		redacted, _ := r.redactor.Redact(rec.Response)
		rec.Response = redacted
	}
	if err := r.w.Append(rec); err != nil && r.logger != nil {
		r.logger.Warn("capture append failed",
			logging.Any("err", err),
			logging.Any("phase", call.Phase),
			logging.Any("chunk_index", call.ChunkIndex),
		)
	}
}

// resolveCaptureRetain normalizes the configured retention count.
// 0 selects the default; negative values keep every run directory.
func resolveCaptureRetain(cfg config.CaptureConfig) int {
	if cfg.RetainRuns == 0 {
		return config.DefaultCaptureRetainRuns
	}
	return cfg.RetainRuns
}

// openCaptureWriter creates this run's capture directory and metadata.
// It returns nil when capture is disabled or setup fails (the caller logs).
func openCaptureWriter(discovery discoveryResult, opts Options, runID, systemMsg, sandboxMode string, logger *logging.Logger) *capture.Writer {
	cfg := discovery.appConfig.Analyzer.Capture
	if !cfg.CaptureEnabled() {
		return nil
	}
	dir := capture.RunDir(discovery.outputRoot, discovery.projectName, runID)
	meta := capture.RunMeta{
		RunID:         runID,
		ProjectName:   discovery.projectName,
		ProjectPath:   discovery.projectPath,
		ProviderID:    discovery.providerID,
		Model:         discovery.providerBlock.Model,
		Sandbox:       sandboxMode,
		SystemMessage: systemMsg,
		StartedAt:     time.Now().UTC(),
		Since:         opts.Since,
		Kind:          capture.KindAnalysis,
	}
	w, err := capture.Open(dir, meta)
	if err != nil {
		logger.Warn("capture setup failed — continuing without LLM capture", logging.Any("err", err))
		return nil
	}
	w.SetMaxFieldKB(cfg.MaxRecordKB)
	if retain := resolveCaptureRetain(cfg); retain > 0 {
		if err := capture.Prune(capture.RunsRoot(discovery.outputRoot, discovery.projectName), retain); err != nil {
			logger.Warn("capture prune failed", logging.Any("err", err))
		}
	}
	logger.Info("capture enabled", logging.Any("dir", dir))
	return w
}

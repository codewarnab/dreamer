package mcpserver

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer creates an MCP server with the record_finding tool registered.
func NewServer(outputPath string) (*mcp.Server, *FindingRecorder, error) {
	recorder, err := NewFindingRecorder(outputPath)
	if err != nil {
		return nil, nil, err
	}

	s := mcp.NewServer(&mcp.Implementation{
		Name:    "dreamer",
		Version: "1.0.0",
	}, nil)

	handler := &findingHandler{recorder: recorder}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "record_finding",
		Description: "Record a validated finding from the analysis. Call this once per finding you want to record. Returns the total count of recorded findings so far.",
	}, handler.RecordFinding)

	return s, recorder, nil
}

// findingHandler holds the recorder state for the MCP tool handler.
type findingHandler struct {
	recorder *FindingRecorder
}

// RecordFinding is the MCP tool handler for record_finding.
func (h *findingHandler) RecordFinding(_ context.Context, _ *mcp.CallToolRequest, input FindingInput) (*mcp.CallToolResult, RecordResult, error) {
	total, err := h.recorder.Record(&input)
	if err != nil {
		return nil, RecordResult{OK: false, Error: err.Error()}, err
	}
	return nil, RecordResult{OK: true, Total: total}, nil
}

// RunMCPServer starts the MCP server on stdio transport and blocks until the
// client disconnects or the process receives a signal.
func RunMCPServer(outputPath string) error {
	s, recorder, err := NewServer(outputPath)
	if err != nil {
		return fmt.Errorf("create MCP server: %w", err)
	}
	defer recorder.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil {
		// Context cancellation from signal is expected shutdown.
		if ctx.Err() != nil {
			log.Printf("MCP server shut down (signal)")
			return nil
		}
		return fmt.Errorf("MCP server run: %w", err)
	}
	return nil
}

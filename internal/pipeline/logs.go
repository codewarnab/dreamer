package pipeline

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/chat"
	"dreamer/internal/logging"
)

func logDiscoveredSources(logger *logging.Logger, sources []chat.ChatSource) {
	logger.Info("discovery done sources=%d", len(sources))
	if len(sources) == 0 {
		return
	}
	byTool := map[chat.SourceType]int{}
	for _, s := range sources {
		byTool[s.Tool]++
		logger.Info("discovered source tool=%s mtime=%s path=%q",
			s.Tool, s.ModifiedTime.UTC().Format(time.RFC3339), s.Path)
	}
	tools := make([]string, 0, len(byTool))
	for tool, count := range byTool {
		tools = append(tools, fmt.Sprintf("%s=%d", tool, count))
	}
	sort.Strings(tools)
	logger.Info("discovery breakdown %s", strings.Join(tools, " "))
}

func logEnabledRulePacks(logger *logging.Logger, packs []analyzer.RulePack) {
	enabled := make([]string, 0, len(packs))
	disabled := make([]string, 0, len(packs))
	for _, p := range packs {
		if p.Enabled {
			enabled = append(enabled, string(p.Category))
		} else {
			disabled = append(disabled, string(p.Category))
		}
	}
	sort.Strings(enabled)
	sort.Strings(disabled)
	logger.Info("rule packs enabled=[%s] disabled=[%s]",
		strings.Join(enabled, ","), strings.Join(disabled, ","))
}

package pipeline

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"dreamer/internal/chat"
	"dreamer/internal/config"
)

const monthLookbackHours = 30 * 24

// parseLookbackWindow parses a compact lookback value (e.g. "24h", "7d", "1mo").
// "" or "lifetime" (case-insensitive) disable filtering.
func parseLookbackWindow(value string) (time.Duration, bool, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return 0, false, nil
	}
	if config.IsLifetimeSince(trimmedValue) {
		return 0, false, nil
	}

	amountText, unitText, ok := splitLookbackValue(trimmedValue)
	if !ok || !isDigitsOnly(amountText) {
		return 0, false, fmt.Errorf("invalid lookback window %q; use a positive integer followed by m, h, d, w, or mo", value)
	}

	amount, err := strconv.Atoi(amountText)
	if err != nil || amount <= 0 {
		return 0, false, fmt.Errorf("invalid lookback window %q; value must be a positive integer", value)
	}

	switch unitText {
	case "m":
		return time.Duration(amount) * time.Minute, true, nil
	case "h":
		return time.Duration(amount) * time.Hour, true, nil
	case "d":
		return time.Duration(amount) * 24 * time.Hour, true, nil
	case "w":
		return time.Duration(amount) * 7 * 24 * time.Hour, true, nil
	case "mo":
		return time.Duration(amount) * monthLookbackHours * time.Hour, true, nil
	default:
		return 0, false, fmt.Errorf("invalid lookback window %q; supported units are m, h, d, w, and mo", value)
	}
}

// filterSourcesByLookback keeps chat sources whose modified time is inside the
// requested lookback window. Sources exactly at the cutoff are included, and
// future timestamps are included to tolerate clock skew across tools.
func filterSourcesByLookback(sources []chat.ChatSource, now time.Time, lookback time.Duration, enabled bool) []chat.ChatSource {
	if !enabled {
		return sources
	}

	cutoff := now.UTC().Add(-lookback)
	filtered := make([]chat.ChatSource, 0, len(sources))
	for _, source := range sources {
		if !source.ModifiedTime.UTC().Before(cutoff) {
			filtered = append(filtered, source)
		}
	}

	return filtered
}

func splitLookbackValue(value string) (string, string, bool) {
	if strings.HasSuffix(value, "mo") {
		return value[:len(value)-2], "mo", len(value) > 2
	}
	if len(value) < 2 {
		return "", "", false
	}

	return value[:len(value)-1], value[len(value)-1:], true
}

func isDigitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

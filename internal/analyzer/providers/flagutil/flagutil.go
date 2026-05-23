// Package flagutil provides helpers for manipulating CLI flag slices
// (e.g. constructing provider command lines with conditional flags).
package flagutil

// ReplaceFlag searches args for the flag matching `flagName` whose current
// value equals `oldValue`, and replaces it with `newValue`.
// Returns the (possibly unchanged) args slice.
func ReplaceFlag(args []string, flagName, oldValue, newValue string) []string {
	for i, a := range args {
		if a == flagName && i+1 < len(args) && args[i+1] == oldValue {
			args[i+1] = newValue
			return args
		}
	}
	return args
}

// AppendToFlag appends `suffix` (with a comma separator) to the value of the
// given flag. If the flag appears multiple times, only the first is modified.
// Returns the (possibly unchanged) args slice.
func AppendToFlag(args []string, flagName, suffix string) []string {
	for i, a := range args {
		if a == flagName && i+1 < len(args) {
			args[i+1] = args[i+1] + "," + suffix
			return args
		}
	}
	return args
}

// RemoveFlag removes the flag and its value from args.
// Handles both `--flag value` and `--flag=value` forms.
// Returns the (possibly unchanged) args slice.
func RemoveFlag(args []string, flagName string) []string {
	var result []string
	for i := 0; i < len(args); i++ {
		// --flag=value combined form
		if len(args[i]) > len(flagName) && args[i][:len(flagName)+1] == flagName+"=" {
			continue
		}
		if args[i] == flagName {
			if i+1 < len(args) && len(args[i+1]) > 0 && args[i+1][0] != '-' {
				// --flag value form: skip both
				i++
			}
			// --flag=value form: already skipped by not appending
			continue
		}
		result = append(result, args[i])
	}
	return result
}

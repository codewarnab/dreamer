// Package flagutil provides helpers for manipulating CLI flag slices
// (e.g. constructing provider command lines with conditional flags).
//
// Every helper handles both the space-separated form (`--flag value`) and
// the equals form (`--flag=value`) — providers may emit either, and the
// flag rewrites have to survive the difference. Helpers return a new slice
// (or the same slice when nothing changed); they do not mutate the input.
package flagutil

import "strings"

// splitEqualsForm reports whether arg has the shape `--flag=value` for the
// given flag name, and if so returns the value portion. The leading sigil
// (one or two dashes) is part of flagName.
func splitEqualsForm(arg, flagName string) (value string, ok bool) {
	if !strings.HasPrefix(arg, flagName+"=") {
		return "", false
	}
	return arg[len(flagName)+1:], true
}

// ReplaceFlag searches args for the flag whose current value equals
// `oldValue` and replaces it with `newValue`. Handles both forms.
func ReplaceFlag(args []string, flagName, oldValue, newValue string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		if a == flagName && i+1 < len(out) && out[i+1] == oldValue {
			out[i+1] = newValue
			return out
		}
		if v, ok := splitEqualsForm(a, flagName); ok && v == oldValue {
			out[i] = flagName + "=" + newValue
			return out
		}
	}
	return out
}

// AppendToFlag appends `suffix` to the value of the given flag using a
// comma separator. If the existing value is empty the suffix replaces it
// (so we don't end up with a leading comma like `,Bash`). Only the first
// occurrence of the flag is modified.
func AppendToFlag(args []string, flagName, suffix string) []string {
	if suffix == "" {
		return append([]string(nil), args...)
	}
	out := append([]string(nil), args...)
	join := func(current, add string) string {
		if current == "" {
			return add
		}
		return current + "," + add
	}
	for i, a := range out {
		if a == flagName && i+1 < len(out) {
			out[i+1] = join(out[i+1], suffix)
			return out
		}
		if v, ok := splitEqualsForm(a, flagName); ok {
			out[i] = flagName + "=" + join(v, suffix)
			return out
		}
	}
	return out
}

// RemoveFlag removes every occurrence of the flag and its value. Handles
// both forms. Values are always treated as values (never re-parsed as a
// new flag) so leading-dash values like `-1` survive.
func RemoveFlag(args []string, flagName string) []string {
	var result []string
	for i := 0; i < len(args); i++ {
		if args[i] == flagName {
			if i+1 < len(args) {
				i++ // also consume the value
			}
			continue
		}
		if _, ok := splitEqualsForm(args[i], flagName); ok {
			continue
		}
		result = append(result, args[i])
	}
	return result
}

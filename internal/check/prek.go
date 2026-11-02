package check

import "github.com/mich-murphy/verify/internal/state"

// parsePrekOutput turns `prek run --verbose --color never` output into one result per hook
// and the output lines each hook printed, keyed by hook id.
func parsePrekOutput(out []byte) ([]state.HookResult, map[string][]string) {
	return nil, nil
}

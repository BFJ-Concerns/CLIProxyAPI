package helps

import "strings"

// IsClaudeMythos52Model reports Claude Mythos 5.2, the verified-access
// counterpart of Claude Fable 5.2. The Claude Code model catalogue gives it
// Fable 5.2's capabilities (per_turn_effort, per_turn_timing, adaptive thinking
// with the preserved-thinking prefix check) except refusal_fallback, so it is
// handled like Fable 5.2 everywhere but the server-side fallback.
func IsClaudeMythos52Model(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, target := range []string{"mythos-5-2", "mythos-5.2"} {
		idx := strings.Index(m, target)
		if idx == -1 {
			continue
		}
		// "mythos-5-2" must not match a later "mythos-5-20".
		next := idx + len(target)
		if next >= len(m) || m[next] < '0' || m[next] > '9' {
			return true
		}
	}
	return false
}

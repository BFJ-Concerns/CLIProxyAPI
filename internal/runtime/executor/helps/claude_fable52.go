package helps

import "strings"

// IsClaudeFable52Model reports the 5.2 generation of the Fable family: Claude
// Fable 5.2 and its verified-access counterpart Claude Mythos 5.2. Both carry
// the per_turn_effort and per_turn_timing capabilities in the Claude Code
// model catalog, and both enforce the preserved-thinking prefix check for
// every account.
func IsClaudeFable52Model(model string) bool {
	return claudeModelMatchesVersion(model, "fable-5-2", "fable-5.2", "mythos-5-2", "mythos-5.2")
}

// IsClaudeFable52OnlyModel reports Claude Fable 5.2 alone. Claude Code lists
// refusal_fallback for Fable 5.2 but not for Mythos 5.2, so only Fable 5.2
// carries the server-side fallback.
func IsClaudeFable52OnlyModel(model string) bool {
	return claudeModelMatchesVersion(model, "fable-5-2", "fable-5.2")
}

// claudeModelMatchesVersion reports whether the lower-cased model contains one
// of targets not followed by another digit, so "fable-5-2" matches
// "claude-fable-5-2[1m]" but not a later "fable-5-20".
func claudeModelMatchesVersion(model string, targets ...string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, target := range targets {
		idx := strings.Index(m, target)
		if idx == -1 {
			continue
		}
		next := idx + len(target)
		if next >= len(m) || m[next] < '0' || m[next] > '9' {
			return true
		}
	}
	return false
}

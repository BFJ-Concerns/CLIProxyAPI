package helps

import (
	"net/http"
	"strings"
	"time"
)

// claudeFableWindowOnlyRejection reports whether an overage- or Fable-only
// rejection is the Fable window itself running out: 7d_oi rejected while
// overage, which could otherwise carry the request past it, is disabled.
// Such a rejection holds until that window resets, so the model-scoped
// cooldown must last as long as the window rather than fall back to the
// generic backoff.
func claudeFableWindowOnlyRejection(headers http.Header, status7dOI string) bool {
	if status7dOI != "rejected" {
		return false
	}
	overageStatus := strings.ToLower(strings.TrimSpace(getHeaderCaseInsensitive(headers, "Anthropic-Ratelimit-Unified-Overage-Status")))
	overageDisabledReason := strings.TrimSpace(getHeaderCaseInsensitive(headers, "Anthropic-Ratelimit-Unified-Overage-Disabled-Reason"))
	return overageStatus == "rejected" || overageDisabledReason != ""
}

// claudeFableWindowReset returns when the Fable window reopens: its own
// 7d_oi reset. Retry-After is not consulted, because alongside a disabled
// overage it can name the overage billing boundary rather than the window.
func claudeFableWindowReset(headers http.Header, now time.Time) (time.Time, bool) {
	raw := getHeaderCaseInsensitive(headers, "Anthropic-Ratelimit-Unified-7d_oi-Reset")
	if raw == "" {
		return time.Time{}, false
	}
	resetAt, ok := parseUnixOrTimestamp(raw)
	if !ok || !resetAt.After(now) {
		return time.Time{}, false
	}
	return resetAt, true
}

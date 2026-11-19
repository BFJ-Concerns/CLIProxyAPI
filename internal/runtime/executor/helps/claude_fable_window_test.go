package helps

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func fableWindowRejection(now time.Time) http.Header {
	return http.Header{
		"Anthropic-Ratelimit-Unified-Status":                  []string{"rejected"},
		"Anthropic-Ratelimit-Unified-5h-Status":               []string{"allowed"},
		"Anthropic-Ratelimit-Unified-7d-Status":               []string{"allowed"},
		"Anthropic-Ratelimit-Unified-7d_oi-Status":            []string{"rejected"},
		"Anthropic-Ratelimit-Unified-7d_oi-Reset":             []string{strconv.FormatInt(now.Add(80*time.Hour).Unix(), 10)},
		"Anthropic-Ratelimit-Unified-Overage-Disabled-Reason": []string{"out_of_credits"},
		"Retry-After": []string{"120"},
	}
}

func TestFableWindowRejectionCoolsDownUntilWindowReset(t *testing.T) {
	now := time.Unix(1793603466, 0)

	t.Run("7d_oi reset wins over a short Retry-After", func(t *testing.T) {
		got := parseClaudeRateLimitResetWithFuzz(fableWindowRejection(now), now, 0, 0)
		if got == nil || *got != 80*time.Hour {
			t.Fatalf("cooldown = %v, want 80h", got)
		}
		if ClaudeHeadersIndicateUnifiedRateLimitRejection(fableWindowRejection(now)) {
			t.Fatal("Fable-window rejection must stay model-scoped")
		}
	})

	t.Run("no 7d_oi reset keeps the generic backoff", func(t *testing.T) {
		// Retry-After beside a disabled overage can be the billing boundary.
		headers := fableWindowRejection(now)
		headers.Del("Anthropic-Ratelimit-Unified-7d_oi-Reset")
		headers.Set("Retry-After", "97273")
		if got := parseClaudeRateLimitResetWithFuzz(headers, now, 0, 0); got != nil {
			t.Fatalf("cooldown = %v, want nil", *got)
		}
	})

	t.Run("overage status rejected counts as disabled", func(t *testing.T) {
		headers := fableWindowRejection(now)
		headers.Del("Anthropic-Ratelimit-Unified-Overage-Disabled-Reason")
		headers.Set("Anthropic-Ratelimit-Unified-Overage-Status", "rejected")
		got := parseClaudeRateLimitResetWithFuzz(headers, now, 0, 0)
		if got == nil || *got != 80*time.Hour {
			t.Fatalf("cooldown = %v, want 80h", got)
		}
	})

	t.Run("overage not disabled keeps the generic backoff", func(t *testing.T) {
		headers := fableWindowRejection(now)
		headers.Del("Anthropic-Ratelimit-Unified-Overage-Disabled-Reason")
		if got := parseClaudeRateLimitResetWithFuzz(headers, now, 0, 0); got != nil {
			t.Fatalf("cooldown = %v, want nil", *got)
		}
	})
}

package redisqueue

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func TestUsageSpoolTapReceivesQueuedPayload(t *testing.T) {
	SetEnabled(true)
	SetUsageStatisticsEnabled(true)
	t.Cleanup(func() {
		SetUsageSpoolTap(nil)
		SetUsageStatisticsEnabled(false)
		SetEnabled(false)
	})

	var tapped []byte
	var tappedAuthID string
	SetUsageSpoolTap(func(record coreusage.Record, payload []byte) {
		tapped = append([]byte(nil), payload...)
		tappedAuthID = record.AuthID
	})

	plugin := &usageQueuePlugin{}
	plugin.HandleUsage(context.Background(), coreusage.Record{
		Provider:    "claude",
		Model:       "claude-opus-5-5",
		AuthID:      "auth-1",
		AuthIndex:   "idx-1",
		RequestedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Detail:      coreusage.Detail{InputTokens: 5, CacheCreationTokens: 7, CacheReadTokens: 11, OutputTokens: 3},
	})

	if tappedAuthID != "auth-1" {
		t.Fatalf("tapped auth = %q", tappedAuthID)
	}
	queued := PopOldest(1)
	if len(queued) != 1 || string(queued[0]) != string(tapped) {
		t.Fatalf("tap payload differs from queued payload:\n tap=%s\nqueue=%v", tapped, queued)
	}
	var decoded struct {
		AuthIndex string `json:"auth_index"`
		Tokens    struct {
			CacheCreationTokens int64 `json:"cache_creation_tokens"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(tapped, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.AuthIndex != "idx-1" || decoded.Tokens.CacheCreationTokens != 7 {
		t.Fatalf("decoded = %+v", decoded)
	}
}

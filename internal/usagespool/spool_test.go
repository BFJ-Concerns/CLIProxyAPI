package usagespool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testClock struct{ t time.Time }

func (c *testClock) now() time.Time { return c.t }

func openTest(t *testing.T, dir string, clock *testClock) *Spool {
	t.Helper()
	s, err := open(dir, 14*24*time.Hour, clock.now)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s
}

func appendUsage(t *testing.T, s *Spool, payload string) int64 {
	t.Helper()
	seq, err := s.AppendUsage([]byte(payload), Account{AuthIndex: "idx", Email: "a@example.com"})
	if err != nil {
		t.Fatalf("append usage: %v", err)
	}
	return seq
}

func seqsOf(t *testing.T, page Page) []int64 {
	t.Helper()
	out := make([]int64, 0, len(page.Records))
	for _, raw := range page.Records {
		seq, ok := lineSeq(raw)
		if !ok {
			t.Fatalf("record without seq: %s", raw)
		}
		out = append(out, seq)
	}
	return out
}

func equalSeqs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAppendUsageDropsAPIKeyAndAttachesAccount(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	s := openTest(t, t.TempDir(), clock)
	appendUsage(t, s, `{"api_key":"secret","model":"claude-opus-5-5","tokens":{"input_tokens":3}}`)

	page, err := s.Read(KindUsage, 0, 10)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(page.Records))
	}
	var got map[string]any
	if err := json.Unmarshal(page.Records[0], &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := got["api_key"]; ok {
		t.Fatal("api_key written to the spool")
	}
	if got["kind"] != KindUsage || got["seq"].(float64) != 1 || got["model"] != "claude-opus-5-5" {
		t.Fatalf("unexpected record: %v", got)
	}
	account := got["account"].(map[string]any)
	if account["email"] != "a@example.com" || account["auth_index"] != "idx" {
		t.Fatalf("account = %v", account)
	}
}

func TestReadPagesAndAckIsPerKind(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	s := openTest(t, t.TempDir(), clock)
	for i := 0; i < 3; i++ {
		appendUsage(t, s, `{}`)
	}
	if _, err := s.AppendQuota(QuotaReading{PolledAt: clock.t, Provider: "claude", Status: 200, Body: []byte(`{"limits":[]}`)}); err != nil {
		t.Fatalf("append quota: %v", err)
	}
	appendUsage(t, s, `{}`)

	page, err := s.Read(KindUsage, 0, 2)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := seqsOf(t, page); !equalSeqs(got, []int64{1, 2}) || page.NextAfter != 2 || *page.OldestSeq != 1 {
		t.Fatalf("page 1 = %v next=%d", got, page.NextAfter)
	}
	page, _ = s.Read(KindUsage, page.NextAfter, 2)
	if got := seqsOf(t, page); !equalSeqs(got, []int64{3, 5}) || page.NextAfter != 5 {
		t.Fatalf("page 2 = %v next=%d", got, page.NextAfter)
	}
	page, _ = s.Read(KindUsage, page.NextAfter, 2)
	if len(page.Records) != 0 || page.NextAfter != 5 {
		t.Fatalf("page 3 = %v next=%d", seqsOf(t, page), page.NextAfter)
	}

	if err := s.Ack(KindUsage, 3); err != nil {
		t.Fatalf("ack: %v", err)
	}
	page, _ = s.Read(KindUsage, 0, 10)
	if got := seqsOf(t, page); !equalSeqs(got, []int64{5}) || *page.OldestSeq != 5 {
		t.Fatalf("after ack = %v oldest=%v", got, page.OldestSeq)
	}
	quota, _ := s.Read(KindQuota, 0, 10)
	if got := seqsOf(t, quota); !equalSeqs(got, []int64{4}) {
		t.Fatalf("quota after usage ack = %v", got)
	}
	if page.PrunedThrough != nil {
		t.Fatalf("pruned_through = %d, want nil", *page.PrunedThrough)
	}
}

func TestRestartPreservesSeqAndUnackedRecords(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	s := openTest(t, dir, clock)
	appendUsage(t, s, `{}`)
	appendUsage(t, s, `{}`)
	if err := s.Ack(KindUsage, 1); err != nil {
		t.Fatalf("ack: %v", err)
	}
	// No Close: simulate a crash after the fsynced appends.

	s2 := openTest(t, dir, clock)
	page, _ := s2.Read(KindUsage, 0, 10)
	if got := seqsOf(t, page); !equalSeqs(got, []int64{2}) {
		t.Fatalf("after restart = %v", got)
	}
	if seq := appendUsage(t, s2, `{}`); seq != 3 {
		t.Fatalf("seq after restart = %d, want 3", seq)
	}
}

func TestSeqNeverRepeatsAfterEverythingIsAcked(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	s := openTest(t, dir, clock)
	appendUsage(t, s, `{}`)
	appendUsage(t, s, `{}`)
	if err := s.Ack(KindUsage, 2); err != nil {
		t.Fatalf("ack: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, KindUsage))
	if len(entries) != 0 {
		t.Fatalf("fully acked segments left on disk: %d", len(entries))
	}

	s2 := openTest(t, dir, clock)
	if seq := appendUsage(t, s2, `{}`); seq != 3 {
		t.Fatalf("seq after full ack and restart = %d, want 3", seq)
	}
}

func TestTornFinalLineIsTruncatedOnOpen(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	s := openTest(t, dir, clock)
	appendUsage(t, s, `{}`)
	seg := s.segments[KindUsage][0].path
	f, err := os.OpenFile(seg, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open segment: %v", err)
	}
	if _, err := f.WriteString(`{"seq":2,"kind":"us`); err != nil {
		t.Fatalf("write torn line: %v", err)
	}
	_ = f.Close()

	s2 := openTest(t, dir, clock)
	page, _ := s2.Read(KindUsage, 0, 10)
	if got := seqsOf(t, page); !equalSeqs(got, []int64{1}) {
		t.Fatalf("after torn line = %v", got)
	}
	if seq := appendUsage(t, s2, `{}`); seq != 2 {
		t.Fatalf("seq after torn line = %d, want 2", seq)
	}
	page, _ = s2.Read(KindUsage, 0, 10)
	if got := seqsOf(t, page); !equalSeqs(got, []int64{1, 2}) {
		t.Fatalf("after append = %v", got)
	}
}

func TestRetentionPrunesUnreadAndReportsPrunedThrough(t *testing.T) {
	dir := t.TempDir()
	clock := &testClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	s := openTest(t, dir, clock)
	appendUsage(t, s, `{}`)
	appendUsage(t, s, `{}`)

	// A new UTC day rotates to a fresh segment.
	clock.t = clock.t.Add(24 * time.Hour)
	appendUsage(t, s, `{}`)
	if n := len(s.segments[KindUsage]); n != 2 {
		t.Fatalf("segments = %d, want 2", n)
	}

	clock.t = time.Date(2026, 10, 15, 13, 0, 0, 0, time.UTC)
	if err := s.Prune(); err != nil {
		t.Fatalf("prune: %v", err)
	}
	page, _ := s.Read(KindUsage, 0, 10)
	if got := seqsOf(t, page); !equalSeqs(got, []int64{3}) {
		t.Fatalf("after prune = %v", got)
	}
	if page.PrunedThrough == nil || *page.PrunedThrough != 2 {
		t.Fatalf("pruned_through = %v, want 2", page.PrunedThrough)
	}

	s2 := openTest(t, dir, clock)
	page, _ = s2.Read(KindUsage, 0, 10)
	if page.PrunedThrough == nil || *page.PrunedThrough != 2 {
		t.Fatalf("pruned_through after restart = %v, want 2", page.PrunedThrough)
	}
}

func TestAckedRecordsDoNotCountAsPruned(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	s := openTest(t, t.TempDir(), clock)
	appendUsage(t, s, `{}`)
	appendUsage(t, s, `{}`)
	if err := s.Ack(KindUsage, 1); err != nil {
		t.Fatalf("ack: %v", err)
	}
	clock.t = clock.t.Add(15 * 24 * time.Hour)
	if err := s.Prune(); err != nil {
		t.Fatalf("prune: %v", err)
	}
	page, _ := s.Read(KindUsage, 0, 10)
	if page.PrunedThrough == nil || *page.PrunedThrough != 2 {
		t.Fatalf("pruned_through = %v, want 2", page.PrunedThrough)
	}

	clock2 := &testClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	s2 := openTest(t, t.TempDir(), clock2)
	appendUsage(t, s2, `{}`)
	if err := s2.Ack(KindUsage, 1); err != nil {
		t.Fatalf("ack: %v", err)
	}
	appendUsage(t, s2, `{}`)
	if err := s2.Ack(KindUsage, 2); err != nil {
		t.Fatalf("ack: %v", err)
	}
	clock2.t = clock2.t.Add(15 * 24 * time.Hour)
	_ = s2.Prune()
	page, _ = s2.Read(KindUsage, 0, 10)
	if page.PrunedThrough != nil {
		t.Fatalf("pruned_through = %d for fully acked spool, want nil", *page.PrunedThrough)
	}
}

func TestQuotaReadingShapes(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	s := openTest(t, t.TempDir(), clock)
	body := `{"limits":[{"kind":"weekly_scoped","percent":12}],"seven_day_breakdown":{"x":1}}`
	if _, err := s.AppendQuota(QuotaReading{PolledAt: clock.t, Provider: "claude", Status: 200, Body: []byte(body)}); err != nil {
		t.Fatalf("append: %v", err)
	}
	retryAt := clock.t.Add(10 * time.Minute)
	if _, err := s.AppendQuota(QuotaReading{PolledAt: clock.t, Provider: "claude", Status: 429, Throttled: true, RetryAt: retryAt}); err != nil {
		t.Fatalf("append: %v", err)
	}
	page, _ := s.Read(KindQuota, 0, 10)
	var ok, throttled map[string]json.RawMessage
	_ = json.Unmarshal(page.Records[0], &ok)
	_ = json.Unmarshal(page.Records[1], &throttled)
	if string(ok["body"]) != body {
		t.Fatalf("body not verbatim: %s", ok["body"])
	}
	if _, hasBody := throttled["body"]; hasBody {
		t.Fatalf("throttled reading carries a body: %s", page.Records[1])
	}
	if string(throttled["throttled"]) != "true" || string(throttled["retry_at"]) != `"2026-10-01T12:10:00Z"` {
		t.Fatalf("throttled reading = %s", page.Records[1])
	}
}

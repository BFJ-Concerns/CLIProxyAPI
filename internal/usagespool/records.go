// Fork addition (usage spool): the two record shapes the spool stores and
// the process-wide spool the producers and management endpoints share.
package usagespool

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"
)

// Account identifies the credential a record belongs to in a form that
// survives an auth file being renamed or re-added (auth_index alone is a
// hash of the auth ID, which follows the file name).
type Account struct {
	AuthID    string `json:"auth_id,omitempty"`
	AuthIndex string `json:"auth_index,omitempty"`
	Email     string `json:"email,omitempty"`
	Label     string `json:"label,omitempty"`
	File      string `json:"file,omitempty"`
}

// usageDroppedFields are queue-record fields never written to disk:
// api_key is the raw client credential the request authenticated with.
var usageDroppedFields = []string{"api_key"}

// AppendUsage spools one usage-queue record (the JSON object the in-memory
// queue carries) with the account attached and secret fields removed.
func (s *Spool) AppendUsage(payload []byte, account Account) (int64, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return 0, fmt.Errorf("usage spool: decode usage record: %w", err)
	}
	for _, name := range usageDroppedFields {
		delete(fields, name)
	}
	accountJSON, err := json.Marshal(account)
	if err != nil {
		return 0, err
	}
	fields["account"] = accountJSON
	return s.Append(KindUsage, fields)
}

// QuotaReading is one quota-poll attempt against a provider's usage
// endpoint. Body holds the response verbatim on a 2xx with a JSON body;
// throttled and failed attempts carry status (0 when no response arrived)
// and the error, never a body, so a gap reads as a failure rather than as
// zero use.
type QuotaReading struct {
	PolledAt  time.Time
	Provider  string
	Account   Account
	Status    int
	Throttled bool
	Failed    bool
	Error     string
	RetryAt   time.Time
	Body      []byte
}

// AppendQuota spools one quota reading.
func (s *Spool) AppendQuota(reading QuotaReading) (int64, error) {
	record := struct {
		PolledAt  time.Time       `json:"polled_at"`
		Provider  string          `json:"provider"`
		Account   Account         `json:"account"`
		Status    int             `json:"status"`
		Throttled bool            `json:"throttled"`
		Failed    bool            `json:"failed"`
		Error     string          `json:"error,omitempty"`
		RetryAt   *time.Time      `json:"retry_at,omitempty"`
		Body      json.RawMessage `json:"body,omitempty"`
	}{
		PolledAt:  reading.PolledAt.UTC(),
		Provider:  reading.Provider,
		Account:   reading.Account,
		Status:    reading.Status,
		Throttled: reading.Throttled,
		Failed:    reading.Failed,
		Error:     reading.Error,
	}
	if !reading.RetryAt.IsZero() {
		retryAt := reading.RetryAt.UTC()
		record.RetryAt = &retryAt
	}
	if len(reading.Body) > 0 && json.Valid(reading.Body) {
		record.Body = json.RawMessage(reading.Body)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return 0, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return 0, err
	}
	return s.Append(KindQuota, fields)
}

var defaultSpool atomic.Pointer[Spool]

// SetDefault installs the process-wide spool (nil uninstalls) and returns
// the one it replaced.
func SetDefault(s *Spool) *Spool { return defaultSpool.Swap(s) }

// Default returns the process-wide spool, or nil when spooling is off.
func Default() *Spool { return defaultSpool.Load() }

// Fork addition (usage spool): every quota-poll attempt, successful or
// not, is offered to an optional sink so it can be recorded durably. The
// poller itself keeps no history; it only reports what each attempt saw.
package auth

import (
	"sync/atomic"
	"time"
)

// QuotaReading is one quota-poll attempt. Body is the usage endpoint's
// response verbatim and is set only on a 2xx; Status is 0 when no
// response arrived (no token, request build or transport failure).
type QuotaReading struct {
	PolledAt  time.Time
	Status    int
	Throttled bool
	Failed    bool
	Err       string
	RetryAt   time.Time
	Body      []byte
}

// QuotaReadingSink receives each attempt with the credential it was made
// for. It is called from poll goroutines and must not block for long.
type QuotaReadingSink func(auth *Auth, reading QuotaReading)

var quotaReadingSink atomic.Pointer[QuotaReadingSink]

// SetQuotaReadingSink installs the sink (nil removes it).
func SetQuotaReadingSink(sink QuotaReadingSink) {
	if sink == nil {
		quotaReadingSink.Store(nil)
		return
	}
	quotaReadingSink.Store(&sink)
}

func reportQuotaReading(auth *Auth, reading QuotaReading) {
	if sink := quotaReadingSink.Load(); sink != nil && auth != nil {
		(*sink)(auth, reading)
	}
}

// Fork addition (usage spool): hands every usage record the queue builds
// to an optional tap before it is enqueued, so a durable copy is taken
// independently of whatever later drains the queue.
package redisqueue

import (
	"sync/atomic"

	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

// UsageSpoolTap receives the source record and the exact JSON payload the
// queue carries. It runs on the usage dispatch path and must not retain
// payload after returning.
type UsageSpoolTap func(record coreusage.Record, payload []byte)

var usageSpoolTap atomic.Pointer[UsageSpoolTap]

// SetUsageSpoolTap installs the tap (nil removes it).
func SetUsageSpoolTap(tap UsageSpoolTap) {
	if tap == nil {
		usageSpoolTap.Store(nil)
		return
	}
	usageSpoolTap.Store(&tap)
}

func tapUsageSpool(record coreusage.Record, payload []byte) {
	if tap := usageSpoolTap.Load(); tap != nil {
		(*tap)(record, payload)
	}
}

// Fork addition (quota-aware routing): the per-credential usage poller.
// It polls Anthropic's OAuth usage endpoint for every loaded Claude
// subscription credential with that credential's own access token (kept
// fresh by the manager's existing auto-refresh loop), feeds the
// selector's usage store, and reports every attempt to the optional
// quota-reading sink (quota_readings.go). Polling is polite and adaptive: sparser when
// every window is comfortable, denser as any window nears exhaustion,
// never more than once per 60 seconds per account, honouring Retry-After
// on 429 in full, and never abandoning a loaded credential. Schedule and
// state survive config reloads and process restarts, so the cadence floor
// and throttle suppression hold per account.
package auth

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moego/json"
	log "github.com/sirupsen/logrus"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/proxyutil"
)

const (
	quotaUsageEndpoint = "https://api.anthropic.com/api/oauth/usage"
	quotaUsageBeta     = "oauth-2025-04-20"

	// quotaPollFloor is the hard per-account cadence floor.
	quotaPollFloor = 60 * time.Second
	// quotaPollTick is how often the scheduler wakes to check what is due.
	quotaPollTick = 15 * time.Second
	// quotaThrottleFallback applies when a 429 carries no usable
	// Retry-After. A usable Retry-After is honoured in full, however long
	// (polling inside an unexpired window is never allowed); the state a long
	// suppression leaves behind ages out through the degradation rule.
	quotaThrottleFallback = 5 * time.Minute
	// quotaFailureRetry schedules the next attempt after a failed poll.
	quotaFailureRetry = 5 * time.Minute
	// quotaFailureDegradeThreshold is how many consecutive failed polls
	// (throttling is not failure) degrade a credential to the
	// no-state-yet rule: its recorded state is dropped so selection stops
	// trusting it. At the failure-retry cadence this is about an hour.
	quotaFailureDegradeThreshold = 12
)

// quotaPollInterval maps the account's tightest window to a poll cadence:
// tens of minutes while comfortable, tightening toward the floor as any
// window approaches exhaustion.
func quotaPollInterval(usage QuotaUsage, now time.Time) time.Duration {
	peak := usage.Session.UtilizationAt(now)
	if u := usage.Weekly.UtilizationAt(now); u > peak {
		peak = u
	}
	if u := usage.Fable.UtilizationAt(now); u > peak {
		peak = u
	}
	switch {
	case peak < 50:
		return 30 * time.Minute
	case peak < 75:
		return 15 * time.Minute
	case peak < 90:
		return 5 * time.Minute
	case peak < 97:
		return 2 * time.Minute
	default:
		return quotaPollFloor
	}
}

// quotaPollSchedule is one credential's polling state. Entries persist for
// the poller's lifetime even while the credential is unloaded or disabled,
// so a quick unload/reload cannot dodge the cadence floor or an unexpired
// Retry-After window.
type quotaPollSchedule struct {
	nextPollAt time.Time
	inFlight   bool
	// failures counts consecutive failed polls (not throttles); reaching
	// quotaFailureDegradeThreshold drops the credential's stored state.
	failures int
}

// quotaUsagePoller runs the polling loop for one manager's credentials.
type quotaUsagePoller struct {
	manager *Manager
	store   *QuotaUsageStore

	ctx    context.Context
	cancel context.CancelFunc

	mu        sync.Mutex
	schedule  map[string]*quotaPollSchedule
	statePath string

	// clients caches one HTTP client per proxy URL ("" = direct). Requests
	// deliberately carry no timeout (upstream law: timeouts only during
	// credential acquisition); a hung request cannot stall other accounts
	// because each poll runs in its own goroutine behind an in-flight flag.
	clientsMu sync.Mutex
	clients   map[string]*http.Client
}

func newQuotaUsagePoller(manager *Manager, store *QuotaUsageStore) *quotaUsagePoller {
	ctx, cancel := context.WithCancel(context.Background())
	return &quotaUsagePoller{
		manager:  manager,
		store:    store,
		ctx:      ctx,
		cancel:   cancel,
		schedule: make(map[string]*quotaPollSchedule),
		clients:  make(map[string]*http.Client),
	}
}

// adopt carries the previous poller's per-account schedule (floor and
// Retry-After suppression) and its stored window state into this poller,
// so a selector reinstall neither re-polls inside the floor nor forgets
// state selection was already trusting.
func (p *quotaUsagePoller) adopt(previous *quotaUsagePoller) {
	if p == nil || previous == nil {
		return
	}
	previous.mu.Lock()
	defer previous.mu.Unlock()
	for id, sched := range previous.schedule {
		p.schedule[id] = &quotaPollSchedule{nextPollAt: sched.nextPollAt, failures: sched.failures}
	}
	if previous.store != p.store {
		for id, usage := range previous.store.Snapshot() {
			p.store.Set(id, usage)
		}
	}
}

// Start launches the polling loop; it runs until Stop.
func (p *quotaUsagePoller) Start() {
	if p == nil {
		return
	}
	go p.run()
}

// Stop cancels the polling loop and any in-flight requests.
func (p *quotaUsagePoller) Stop() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancel()
}

func (p *quotaUsagePoller) run() {
	log.Info("quota usage poller started")
	p.sweep(time.Now())
	ticker := time.NewTicker(quotaPollTick)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			log.Info("quota usage poller stopped")
			return
		case <-ticker.C:
			p.sweep(time.Now())
		}
	}
}

// sweep launches a poll for every enabled, loaded Claude subscription
// credential that is due, and prunes stored state for credentials no
// longer loaded (also healing any state an in-flight poll wrote after its
// credential was unloaded — within one tick).
func (p *quotaUsagePoller) sweep(now time.Time) {
	if p.manager == nil {
		return
	}
	loaded := make(map[string]*Auth)
	for _, auth := range p.manager.List() {
		if isClaudeSubscriptionAuth(auth) {
			loaded[auth.ID] = auth
		}
	}

	p.mu.Lock()
	if p.ctx.Err() != nil {
		p.mu.Unlock()
		return
	}
	pruned := false
	for _, id := range p.store.IDs() {
		if _, ok := loaded[id]; !ok {
			p.store.Remove(id)
			pruned = true
		}
	}
	if pruned {
		p.saveStateLocked()
	}
	due := make([]*Auth, 0, len(loaded))
	for id, auth := range loaded {
		sched, ok := p.schedule[id]
		if !ok {
			sched = &quotaPollSchedule{}
			p.schedule[id] = sched
		}
		if auth.Disabled || sched.inFlight || now.Before(sched.nextPollAt) {
			continue
		}
		sched.inFlight = true
		due = append(due, auth)
	}
	p.mu.Unlock()

	for _, auth := range due {
		go p.poll(auth, time.Now())
	}
}

// begin records the floor before sending a request so an interrupted poll
// still counts towards the cadence limit across a restart.
func (p *quotaUsagePoller) begin(authID string, startedAt time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx.Err() != nil {
		return false
	}
	p.schedule[authID].nextPollAt = startedAt.Add(quotaPollFloor)
	p.saveStateLocked()
	return true
}

// finish records the reading and scheduling outcome together. Failed polls
// keep the previous reading until the failure threshold is reached.
func (p *quotaUsagePoller) finish(authID string, startedAt, nextPollAt time.Time, failed bool, usage *QuotaUsage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx.Err() != nil {
		return
	}
	sched := p.schedule[authID]
	sched.inFlight = false
	sched.nextPollAt = nextPollAt
	if floor := startedAt.Add(quotaPollFloor); nextPollAt.Before(floor) {
		sched.nextPollAt = floor
	}
	if failed {
		sched.failures++
		if sched.failures == quotaFailureDegradeThreshold {
			p.store.Remove(authID)
			log.WithField("auth", authID).Warn("quota polls persistently failing; degrading credential to no-state routing")
		}
	} else {
		sched.failures = 0
	}
	if usage != nil {
		p.store.Set(authID, *usage)
	}
	p.saveStateLocked()
}

// poll fetches one credential's usage state. Failures keep the last known
// state serving (the store is only written on success) until the
// degradation threshold drops it.
func (p *quotaUsagePoller) poll(auth *Auth, now time.Time) {
	if !p.begin(auth.ID, now) {
		return
	}
	accessToken, _ := auth.Metadata["access_token"].(string)
	if strings.TrimSpace(accessToken) == "" {
		log.WithFields(log.Fields{"auth": auth.ID}).Debug("quota poll skipped: no access token in metadata")
		p.finish(auth.ID, now, now.Add(quotaFailureRetry), true, nil)
		reportQuotaReading(auth, QuotaReading{PolledAt: now, Failed: true, Err: "no access token in metadata"})
		return
	}

	req, errRequest := http.NewRequestWithContext(p.ctx, http.MethodGet, quotaUsageEndpoint, nil)
	if errRequest != nil {
		log.WithFields(log.Fields{"auth": auth.ID, "error": errRequest.Error()}).Warn("quota poll request build failed")
		p.finish(auth.ID, now, now.Add(quotaFailureRetry), true, nil)
		reportQuotaReading(auth, QuotaReading{PolledAt: now, Failed: true, Err: errRequest.Error()})
		return
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-beta", quotaUsageBeta)
	req.Header.Set("Content-Type", "application/json")

	resp, errDo := p.clientFor(auth.ProxyURL).Do(req)
	if errDo != nil {
		log.WithFields(log.Fields{"auth": auth.ID, "error": errDo.Error()}).Warn("quota poll failed; serving last known state")
		p.finish(auth.ID, now, now.Add(quotaFailureRetry), true, nil)
		reportQuotaReading(auth, QuotaReading{PolledAt: now, Failed: true, Err: errDo.Error()})
		return
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("quota poll body close: %v", errClose)
		}
	}()

	if resp.StatusCode == http.StatusTooManyRequests {
		retryAt := quotaRetryAt(resp.Header, now)
		log.WithFields(log.Fields{"auth": auth.ID, "retry_at": retryAt.Format(time.RFC3339)}).Info("quota poll throttled; honouring Retry-After")
		p.finish(auth.ID, now, retryAt, false, nil)
		reportQuotaReading(auth, QuotaReading{PolledAt: now, Status: resp.StatusCode, Throttled: true, RetryAt: retryAt})
		return
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		log.WithFields(log.Fields{"auth": auth.ID, "status": resp.StatusCode}).Warn("quota poll returned non-success; serving last known state")
		p.finish(auth.ID, now, now.Add(quotaFailureRetry), true, nil)
		reportQuotaReading(auth, QuotaReading{PolledAt: now, Status: resp.StatusCode, Failed: true})
		return
	}

	body, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		log.WithFields(log.Fields{"auth": auth.ID, "error": errRead.Error()}).Warn("quota poll read failed; serving last known state")
		p.finish(auth.ID, now, now.Add(quotaFailureRetry), true, nil)
		reportQuotaReading(auth, QuotaReading{PolledAt: now, Status: resp.StatusCode, Failed: true, Err: errRead.Error()})
		return
	}
	usage, errParse := parseClaudeUsageResponse(body, time.Now())
	if errParse != nil {
		log.WithFields(log.Fields{"auth": auth.ID, "error": errParse.Error()}).Warn("quota poll parse failed; serving last known state")
		p.finish(auth.ID, now, now.Add(quotaFailureRetry), true, nil)
		reportQuotaReading(auth, QuotaReading{PolledAt: now, Status: resp.StatusCode, Failed: true, Err: errParse.Error(), Body: body})
		return
	}
	p.finish(auth.ID, now, now.Add(quotaPollInterval(usage, now)), false, &usage)
	reportQuotaReading(auth, QuotaReading{PolledAt: now, Status: resp.StatusCode, Body: body})
	log.Infof(
		"quota usage polled auth=%s session=%s weekly=%s fable=%s",
		auth.ID,
		describeQuotaWindow(usage.Session, now),
		describeQuotaWindow(usage.Weekly, now),
		describeQuotaWindow(usage.Fable, now),
	)
}

// clientFor returns the cached HTTP client for a credential's proxy URL,
// building its transport on first use.
func (p *quotaUsagePoller) clientFor(proxyURL string) *http.Client {
	proxyURL = strings.TrimSpace(proxyURL)
	p.clientsMu.Lock()
	defer p.clientsMu.Unlock()
	if client, ok := p.clients[proxyURL]; ok {
		return client
	}
	client := &http.Client{}
	if proxyURL != "" {
		transport, _, errBuild := proxyutil.BuildHTTPTransport(proxyURL)
		if errBuild != nil {
			log.Errorf("quota poller proxy transport: %v", errBuild)
		}
		if transport != nil {
			client.Transport = transport
		}
	}
	p.clients[proxyURL] = client
	return client
}

// quotaRetryAt derives the next allowed poll time from a 429's Retry-After
// header: seconds or HTTP-date, honoured in full however far ahead (polling
// inside an unexpired window is never allowed), falling back to five
// minutes when the header is missing, unreadable, or already past.
func quotaRetryAt(header http.Header, now time.Time) time.Time {
	fallback := now.Add(quotaThrottleFallback)
	raw := strings.TrimSpace(header.Get("Retry-After"))
	if raw == "" {
		return fallback
	}
	var retryAt time.Time
	if seconds, errParse := strconv.Atoi(raw); errParse == nil {
		retryAt = now.Add(time.Duration(seconds) * time.Second)
	} else if parsed, errParse := http.ParseTime(raw); errParse == nil {
		retryAt = parsed
	}
	if !retryAt.After(now) {
		return fallback
	}
	return retryAt
}

// parseClaudeUsageResponse maps the OAuth usage endpoint's body to window
// state: the unified `limits` array (kinds session, weekly_all, and the
// model-scoped weekly_scoped) with the legacy five_hour/seven_day fields
// as fallback. When several limits share a kind, the fullest wins.
func parseClaudeUsageResponse(body []byte, polledAt time.Time) (QuotaUsage, error) {
	var data struct {
		FiveHour *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay *struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day"`
		Limits []struct {
			Kind     string  `json:"kind"`
			Percent  float64 `json:"percent"`
			ResetsAt string  `json:"resets_at"`
		} `json:"limits"`
	}
	if errUnmarshal := json.Unmarshal(body, &data); errUnmarshal != nil {
		return QuotaUsage{}, errUnmarshal
	}

	usage := QuotaUsage{UpdatedAt: polledAt}
	fuller := func(current *QuotaWindow, candidate *QuotaWindow) *QuotaWindow {
		if current == nil || candidate.Percent > current.Percent {
			return candidate
		}
		return current
	}
	for _, limit := range data.Limits {
		candidate := &QuotaWindow{Percent: limit.Percent, ResetsAt: parseQuotaResetTime(limit.ResetsAt)}
		switch limit.Kind {
		case "session":
			usage.Session = fuller(usage.Session, candidate)
		case "weekly_all":
			usage.Weekly = fuller(usage.Weekly, candidate)
		case "weekly_scoped":
			usage.Fable = fuller(usage.Fable, candidate)
		}
	}
	if usage.Session == nil && data.FiveHour != nil {
		usage.Session = &QuotaWindow{Percent: data.FiveHour.Utilization, ResetsAt: parseQuotaResetTime(data.FiveHour.ResetsAt)}
	}
	if usage.Weekly == nil && data.SevenDay != nil {
		usage.Weekly = &QuotaWindow{Percent: data.SevenDay.Utilization, ResetsAt: parseQuotaResetTime(data.SevenDay.ResetsAt)}
	}
	if usage.Session == nil && usage.Weekly == nil && usage.Fable == nil {
		return QuotaUsage{}, &Error{Code: "quota_usage_empty", Message: "usage response contained no limits"}
	}
	return usage, nil
}

// parseQuotaResetTime accepts RFC3339 or a Unix-seconds string; a value it
// cannot read becomes the zero time (window treated as live at its
// recorded percent).
func parseQuotaResetTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if parsed, errParse := time.Parse(time.RFC3339, raw); errParse == nil {
		return parsed
	}
	if seconds, errParse := strconv.ParseInt(raw, 10, 64); errParse == nil && seconds > 0 {
		return time.Unix(seconds, 0)
	}
	return time.Time{}
}

// quotaPollers tracks the live poller per manager so a selector reinstall
// (config reload, strategy change) replaces rather than leaks it — the
// session-affinity wrapper does not forward Stop to its fallback, so
// upstream's StoppableSelector handling cannot reach a wrapped quota
// selector.
var (
	quotaPollersMu sync.Mutex
	quotaPollers   = make(map[*Manager]*quotaUsagePoller)
)

// BindQuotaPoller restores and starts quota polling for a quota-aware
// selector, unwrapping session affinity. Call it after setting the manager's
// config, and at teardown with a nil selector. It restores state before
// returning so new session bindings can use the last successful readings.
func BindQuotaPoller(m *Manager, installed Selector) {
	if m == nil {
		return
	}
	if wrapper, ok := installed.(*SessionAffinitySelector); ok {
		installed = wrapper.fallback
	}
	quota, ok := installed.(*QuotaAwareSelector)

	quotaPollersMu.Lock()
	defer quotaPollersMu.Unlock()
	previous := quotaPollers[m]
	if previous != nil {
		previous.Stop()
	}
	if !ok {
		delete(quotaPollers, m)
		return
	}

	next := newQuotaUsagePoller(m, quota.store)
	next.statePath = quotaPollStatePath(m)
	if previous != nil && previous.statePath == next.statePath {
		next.adopt(previous)
	} else {
		next.restoreState()
	}
	quotaPollers[m] = next
	quota.bindPoller(next)
	next.Start()
}

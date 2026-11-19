package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/MoeForge/moego/json"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	log "github.com/sirupsen/logrus"
)

// The non-JSON suffix keeps this file out of credential import and discovery.
const quotaPollStateFile = ".quota-poll-state"

type quotaPollState struct {
	NextPollAt time.Time   `json:"next_poll_at"`
	Failures   int         `json:"failures,omitempty"`
	Usage      *QuotaUsage `json:"usage,omitempty"`
}

func quotaPollStatePath(m *Manager) string {
	cfg, _ := m.runtimeConfig.Load().(*internalconfig.Config)
	if cfg == nil || cfg.Home.Enabled {
		return ""
	}
	dir, err := util.ResolveAuthDir(cfg.AuthDir)
	if err != nil {
		log.WithError(err).Warn("quota poll state directory unavailable")
		return ""
	}
	return filepath.Join(dir, quotaPollStateFile)
}

func (p *quotaUsagePoller) restoreState() {
	if p.statePath == "" {
		return
	}
	data, err := os.ReadFile(p.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.WithError(err).Warn("quota poll state could not be read")
		return
	}
	var states map[string]quotaPollState
	if err := json.Unmarshal(data, &states); err != nil {
		log.WithError(err).Warn("quota poll state could not be decoded")
		return
	}
	for id, state := range states {
		p.schedule[id] = &quotaPollSchedule{nextPollAt: state.NextPollAt, failures: state.Failures}
		if state.Usage != nil {
			p.store.Set(id, *state.Usage)
		}
	}
}

// saveStateLocked writes schedule and readings together while p.mu is held.
// The snapshot contains auth IDs and quota state only.
func (p *quotaUsagePoller) saveStateLocked() {
	if p.statePath == "" {
		return
	}
	states := make(map[string]quotaPollState, len(p.schedule))
	for id, schedule := range p.schedule {
		states[id] = quotaPollState{NextPollAt: schedule.nextPollAt, Failures: schedule.failures}
	}
	for id, usage := range p.store.Snapshot() {
		state := states[id]
		state.Usage = &usage
		states[id] = state
	}
	data, err := json.Marshal(states)
	if err == nil {
		err = writeQuotaPollState(p.statePath, data)
	}
	if err != nil {
		log.WithError(err).Warn("quota poll state could not be saved")
	}
}

func writeQuotaPollState(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".quota-poll-*.tmp")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer func() {
		if errRemove := os.Remove(tmp); errRemove != nil && !errors.Is(errRemove, os.ErrNotExist) {
			log.WithError(errRemove).Warn("quota poll state temp file could not be removed")
		}
	}()

	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	if errClose := file.Close(); errClose != nil {
		err = errors.Join(err, fmt.Errorf("close quota poll state temp file: %w", errClose))
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

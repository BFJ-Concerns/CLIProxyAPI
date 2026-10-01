// Fork addition (usage spool): opens the configured spool at service
// start, connects the usage-queue tap and the quota-reading sink to it,
// and closes it at shutdown.
package cliproxy

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/redisqueue"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagespool"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

// startUsageSpool opens the spool when configured. A spool that cannot be
// opened is logged and left off; it never stops the service.
func (s *Service) startUsageSpool() {
	if s == nil || s.cfg == nil || !s.cfg.UsageSpool.Enabled {
		return
	}
	dir := s.cfg.UsageSpool.EffectiveDir()
	if !filepath.IsAbs(dir) && s.configPath != "" {
		dir = filepath.Join(filepath.Dir(s.configPath), dir)
	}
	retention := time.Duration(s.cfg.UsageSpool.EffectiveRetentionDays()) * 24 * time.Hour
	spool, err := usagespool.Open(dir, retention)
	if err != nil {
		log.WithError(err).Error("usage spool disabled: open failed")
		return
	}
	if previous := usagespool.SetDefault(spool); previous != nil {
		if errClose := previous.Close(); errClose != nil {
			log.WithError(errClose).Warn("usage spool: close of previous spool failed")
		}
	}

	redisqueue.SetUsageSpoolTap(func(record coreusage.Record, payload []byte) {
		if _, errAppend := spool.AppendUsage(payload, s.usageSpoolAccount(record.AuthID, record.AuthIndex)); errAppend != nil {
			log.WithError(errAppend).Warn("usage spool: usage record not written")
		}
	})
	coreauth.SetQuotaReadingSink(func(auth *coreauth.Auth, reading coreauth.QuotaReading) {
		_, errAppend := spool.AppendQuota(usagespool.QuotaReading{
			PolledAt:  reading.PolledAt,
			Provider:  auth.Provider,
			Account:   usageSpoolAccountOf(auth),
			Status:    reading.Status,
			Throttled: reading.Throttled,
			Failed:    reading.Failed,
			Error:     reading.Err,
			RetryAt:   reading.RetryAt,
			Body:      reading.Body,
		})
		if errAppend != nil {
			log.WithError(errAppend).Warn("usage spool: quota reading not written")
		}
	})

	log.WithFields(log.Fields{"dir": dir, "retention_days": s.cfg.UsageSpool.EffectiveRetentionDays()}).Info("usage spool enabled")
	if !s.cfg.UsageStatisticsEnabled && !s.cfg.Home.Enabled {
		log.Warn("usage spool: usage-statistics-enabled is false, so only quota readings will be spooled")
	}
}

// stopUsageSpool disconnects the producers and closes the spool.
func (s *Service) stopUsageSpool() {
	redisqueue.SetUsageSpoolTap(nil)
	coreauth.SetQuotaReadingSink(nil)
	if spool := usagespool.SetDefault(nil); spool != nil {
		if err := spool.Close(); err != nil {
			log.WithError(err).Warn("usage spool: close failed")
		}
	}
}

// usageSpoolAccount resolves a usage record's credential; a credential no
// longer loaded keeps the identity the record itself carries.
func (s *Service) usageSpoolAccount(authID, authIndex string) usagespool.Account {
	if s.coreManager != nil && authID != "" {
		if auth, ok := s.coreManager.GetByID(authID); ok {
			return usageSpoolAccountOf(auth)
		}
	}
	return usagespool.Account{AuthID: authID, AuthIndex: authIndex}
}

func usageSpoolAccountOf(auth *coreauth.Auth) usagespool.Account {
	account := usagespool.Account{
		AuthID:    auth.ID,
		AuthIndex: auth.EnsureIndex(),
		Label:     auth.Label,
		File:      auth.FileName,
	}
	if email, ok := auth.Metadata["email"].(string); ok {
		account.Email = strings.TrimSpace(email)
	}
	if account.File == "" && auth.Attributes != nil {
		if path := auth.Attributes["path"]; path != "" {
			account.File = filepath.Base(path)
		}
	}
	return account
}

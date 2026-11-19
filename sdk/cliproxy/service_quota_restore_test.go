package cliproxy

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MoeForge/moego/json"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	sdkauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

const (
	exhaustedQuotaAuth = "quota-restore-a-exhausted.json"
	availableQuotaAuth = "quota-restore-b-available.json"
	dueQuotaAuth       = "quota-restore-c-due.json"
)

type savedQuotaPoll struct {
	NextPollAt time.Time            `json:"next_poll_at"`
	Usage      *coreauth.QuotaUsage `json:"usage,omitempty"`
}

func seedServiceQuotaSnapshot(t *testing.T) (*config.Config, map[string]savedQuotaPoll) {
	t.Helper()
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	saved := map[string]savedQuotaPoll{
		exhaustedQuotaAuth: {
			NextPollAt: now.Add(10 * time.Minute),
			Usage: &coreauth.QuotaUsage{
				Weekly:    &coreauth.QuotaWindow{Percent: 100, ResetsAt: now.Add(2 * time.Hour)},
				UpdatedAt: now.Add(-time.Minute),
			},
		},
		availableQuotaAuth: {
			NextPollAt: now.Add(20 * time.Minute),
			Usage: &coreauth.QuotaUsage{
				Weekly:    &coreauth.QuotaWindow{Percent: 20, ResetsAt: now.Add(4 * time.Hour)},
				UpdatedAt: now.Add(-time.Minute),
			},
		},
	}
	for id := range saved {
		writeQuotaRestoreAuth(t, dir, id)
	}
	data, errMarshal := json.Marshal(saved)
	if errMarshal != nil {
		t.Fatalf("marshal quota snapshot: %v", errMarshal)
	}
	if errWrite := os.WriteFile(filepath.Join(dir, ".quota-poll-state"), data, 0o600); errWrite != nil {
		t.Fatalf("write quota snapshot: %v", errWrite)
	}
	return &config.Config{
		Host:    "127.0.0.1",
		AuthDir: dir,
		Routing: config.RoutingConfig{Strategy: "quota-aware", SessionAffinity: true},
	}, saved
}

func writeQuotaRestoreAuth(t *testing.T, dir, id string) {
	t.Helper()
	// These credentials can be selected as OAuth accounts without a token
	// that would allow an outbound request.
	if errWrite := os.WriteFile(filepath.Join(dir, id), []byte(`{"type":"claude","auth_kind":"oauth"}`), 0o600); errWrite != nil {
		t.Fatalf("write auth %s: %v", id, errWrite)
	}
}

func assertServiceQuotaRoutingRestored(t *testing.T, manager *coreauth.Manager) {
	t.Helper()
	var candidates []*coreauth.Auth
	for _, id := range []string{exhaustedQuotaAuth, availableQuotaAuth} {
		auth, ok := manager.GetByID(id)
		if !ok || auth.AuthKind() != coreauth.AuthKindOAuth {
			t.Fatalf("OAuth credential %s was not loaded", id)
		}
		candidates = append(candidates, auth)
	}
	opts := cliproxyexecutor.Options{Headers: http.Header{"X-Session-Id": {"quota-restore-new-session"}}}
	chosen, errPick := manager.Selector().Pick(context.Background(), "claude", "claude-fable-5", opts, candidates)
	if errPick != nil {
		t.Fatalf("pick for cold Fable session: %v", errPick)
	}
	// The default rotation starts with the exhausted account. With both
	// readings available, quota-aware routing skips it.
	if chosen == nil || chosen.ID != availableQuotaAuth {
		t.Fatalf("cold Fable session chose %v, want %s from saved usage", chosen, availableQuotaAuth)
	}
}

func TestServiceRun_RestoresQuotaReadingsAndPollDeadlines(t *testing.T) {
	cfg, saved := seedServiceQuotaSnapshot(t)
	writeQuotaRestoreAuth(t, cfg.AuthDir, dueQuotaAuth)
	registry.SetLocalModelCatalogs(true)
	t.Cleanup(func() { registry.SetLocalModelCatalogs(false) })
	cfg.ProxyURL = "http://127.0.0.1:1"

	previousStore := sdkauth.GetTokenStore()
	sdkauth.RegisterTokenStore(sdkauth.NewFileTokenStore())
	t.Cleanup(func() { sdkauth.RegisterTokenStore(previousStore) })

	pollFinished := make(chan struct{}, 1)
	coreauth.SetQuotaReadingSink(func(auth *coreauth.Auth, _ coreauth.QuotaReading) {
		if auth.ID == dueQuotaAuth {
			select {
			case pollFinished <- struct{}{}:
			default:
			}
		}
	})
	t.Cleanup(func() { coreauth.SetQuotaReadingSink(nil) })

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if errWrite := os.WriteFile(configPath, []byte("{}"), 0o600); errWrite != nil {
		t.Fatalf("write config file: %v", errWrite)
	}
	beforeStart := make(chan struct{})
	service, errBuild := NewBuilder().
		WithConfig(cfg).
		WithConfigPath(configPath).
		WithHooks(Hooks{OnBeforeStart: func(*config.Config) { close(beforeStart) }}).
		Build()
	if errBuild != nil {
		t.Fatalf("build service: %v", errBuild)
	}

	t.Cleanup(func() {
		registry.SetModelRefreshCallback(nil)
		for id := range saved {
			registry.GetGlobalRegistry().UnregisterClient(id)
		}
		registry.GetGlobalRegistry().UnregisterClient(dueQuotaAuth)
		service.coreManager.SetSelector(nil)
	})
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	var errRun error
	go func() {
		defer close(runDone)
		errRun = service.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
			if errRun != nil && !errors.Is(errRun, context.Canceled) {
				t.Errorf("run service: %v", errRun)
			}
		case <-time.After(10 * time.Second):
			t.Error("service did not shut down")
		}
	})
	select {
	case <-beforeStart:
	case <-runDone:
		t.Fatalf("service exited before serving requests: %v", errRun)
	case <-time.After(5 * time.Second):
		t.Fatal("service did not reach its startup hook")
	}
	assertServiceQuotaRoutingRestored(t, service.coreManager)

	// The due credential has no token, but the attempt still saves the
	// in-memory schedule. Its new entry proves this is a fresh snapshot.
	select {
	case <-pollFinished:
	case <-time.After(5 * time.Second):
		t.Fatal("startup poller did not attempt the due credential")
	}
	data, errRead := os.ReadFile(filepath.Join(cfg.AuthDir, ".quota-poll-state"))
	if errRead != nil {
		t.Fatalf("read quota snapshot after the first sweep: %v", errRead)
	}
	var persisted map[string]savedQuotaPoll
	if errDecode := json.Unmarshal(data, &persisted); errDecode != nil {
		t.Fatalf("decode quota snapshot after the first sweep: %v", errDecode)
	}
	if persisted[dueQuotaAuth].NextPollAt.IsZero() {
		t.Fatal("first sweep did not save the newly scheduled deadline")
	}
	for id, want := range saved {
		got := persisted[id]
		if !got.NextPollAt.Equal(want.NextPollAt) {
			t.Errorf("%s poll deadline after startup = %v, want %v", id, got.NextPollAt, want.NextPollAt)
		}
		if got.Usage == nil || got.Usage.Weekly == nil {
			t.Errorf("%s weekly reading missing after startup", id)
			continue
		}
		if got.Usage.Weekly.Percent != want.Usage.Weekly.Percent ||
			!got.Usage.Weekly.ResetsAt.Equal(want.Usage.Weekly.ResetsAt) ||
			!got.Usage.UpdatedAt.Equal(want.Usage.UpdatedAt) {
			t.Errorf("%s weekly reading or update time changed after startup", id)
		}
	}
}

func TestServiceApplyManagerConfig_RestoresQuotaFromNewAuthDirectory(t *testing.T) {
	cfg, _ := seedServiceQuotaSnapshot(t)
	store := sdkauth.NewFileTokenStore()
	store.SetBaseDir(cfg.AuthDir)
	manager := coreauth.NewManager(store, &coreauth.RoundRobinSelector{}, nil)
	manager.SetConfig(&config.Config{AuthDir: t.TempDir()})
	if errLoad := manager.Load(context.Background()); errLoad != nil {
		t.Fatalf("load credentials: %v", errLoad)
	}
	t.Cleanup(func() {
		coreauth.BindQuotaPoller(manager, nil)
		manager.SetSelector(nil)
	})

	service := &Service{coreManager: manager}
	if !service.applyManagerConfig(context.Background(), configCommit{cfg: cfg}) {
		t.Fatal("apply manager config failed")
	}
	assertServiceQuotaRoutingRestored(t, manager)
}

package management

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// resetProbeExecutor serves every request successfully and records which
// credential served it.
type resetProbeExecutor struct {
	provider string
	served   []string
}

func (e *resetProbeExecutor) Identifier() string { return e.provider }

func (e *resetProbeExecutor) Execute(_ context.Context, auth *coreauth.Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.served = append(e.served, auth.ID)
	return cliproxyexecutor.Response{Payload: []byte(`{}`)}, nil
}

func (e *resetProbeExecutor) ExecuteStream(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, nil
}

func (e *resetProbeExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *resetProbeExecutor) CountTokens(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (e *resetProbeExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

// TestResetQuota_FreesBenchedCredentialForNextRequest benches a credential
// for a multi-day window — credential-wide and model-scoped, persisted to
// the cooldown store — then resets it through the management endpoint and
// requires the next request to be served by it, in this process and in one
// restored from the store.
func TestResetQuota_FreesBenchedCredentialForNextRequest(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")

	cases := []struct {
		provider string
		model    string
		// selector builds the routing strategy; nil keeps the manager default.
		selector func() coreauth.Selector
	}{
		{provider: "codex", model: "gpt-reset-probe"},
		{
			// Claude subscription credentials route through the quota-aware
			// strategy behind session affinity.
			provider: "claude",
			model:    "claude-reset-probe",
			selector: func() coreauth.Selector {
				return coreauth.NewSessionAffinitySelector(coreauth.NewQuotaAwareSelector(coreauth.QuotaPolicy{SessionCeiling: 95}))
			},
		},
	}
	benches := []struct {
		name            string
		credentialScope bool
	}{
		{name: "credential-wide", credentialScope: true},
		{name: "model-scoped", credentialScope: false},
	}

	for _, tc := range cases {
		for _, bench := range benches {
			t.Run(tc.provider+"/"+bench.name, func(t *testing.T) {
				ctx := context.Background()
				storeDir := t.TempDir()
				authID := "reset-probe-" + tc.provider + "-" + bench.name + ".json"
				registry.GetGlobalRegistry().RegisterClient(authID, tc.provider, []*registry.ModelInfo{{ID: tc.model}})
				t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(authID) })

				newManager := func() (*coreauth.Manager, *resetProbeExecutor) {
					var selector coreauth.Selector
					if tc.selector != nil {
						selector = tc.selector()
					}
					manager := coreauth.NewManager(nil, selector, nil)
					manager.SetCooldownStateStore(coreauth.NewFileCooldownStateStore(storeDir))
					exec := &resetProbeExecutor{provider: tc.provider}
					manager.RegisterExecutor(exec)
					auth := &coreauth.Auth{
						ID:         authID,
						FileName:   authID,
						Provider:   tc.provider,
						Status:     coreauth.StatusActive,
						Attributes: map[string]string{"auth_kind": "oauth"},
						Metadata:   map[string]any{"type": tc.provider, "access_token": "token"},
					}
					if _, errRegister := manager.Register(coreauth.WithSkipPersist(ctx), auth); errRegister != nil {
						t.Fatalf("register auth: %v", errRegister)
					}
					return manager, exec
				}

				manager, exec := newManager()
				window := 68 * time.Hour
				manager.MarkResult(ctx, coreauth.Result{
					AuthID:          authID,
					Provider:        tc.provider,
					Model:           tc.model,
					Success:         false,
					CredentialScope: bench.credentialScope,
					RetryAfter:      &window,
					Error:           &coreauth.Error{HTTPStatus: http.StatusTooManyRequests, Message: "usage limit reached"},
				})
				manager.PersistCooldownStates(ctx)

				records, errLoad := coreauth.NewFileCooldownStateStore(storeDir).Load(ctx)
				if errLoad != nil || len(records) == 0 {
					t.Fatalf("bench was not persisted: records=%d err=%v", len(records), errLoad)
				}
				request := cliproxyexecutor.Request{Model: tc.model, Payload: []byte(`{}`)}
				if _, errBenched := manager.Execute(ctx, []string{tc.provider}, request, cliproxyexecutor.Options{}); errBenched == nil {
					t.Fatalf("benched credential served a request before the reset")
				}

				auth, _ := manager.GetByID(authID)
				authIndex := auth.EnsureIndex()
				h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
				rec := httptest.NewRecorder()
				ginCtx, _ := gin.CreateTestContext(rec)
				ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v8/management/routing/cooldown/reset", strings.NewReader(`{"auth_index":"`+authIndex+`"}`))
				ginCtx.Request.Header.Set("Content-Type", "application/json")
				h.ResetQuota(ginCtx)
				if rec.Code != http.StatusOK {
					t.Fatalf("reset status = %d, body %s", rec.Code, rec.Body.String())
				}

				if _, errServe := manager.Execute(ctx, []string{tc.provider}, request, cliproxyexecutor.Options{}); errServe != nil {
					t.Fatalf("next request after reset failed: %v", errServe)
				}
				if len(exec.served) != 1 || exec.served[0] != authID {
					t.Fatalf("served by %v, want %s", exec.served, authID)
				}

				records, errLoad = coreauth.NewFileCooldownStateStore(storeDir).Load(ctx)
				if errLoad != nil {
					t.Fatalf("load cooldown store: %v", errLoad)
				}
				for _, record := range records {
					if record.AuthID == authID && record.NextRetryAfter.After(time.Now()) {
						t.Fatalf("persisted bench survived the reset: %+v", record)
					}
				}

				restarted, restartedExec := newManager()
				if errRestore := restarted.RestoreCooldownStates(ctx); errRestore != nil {
					t.Fatalf("restore cooldown states: %v", errRestore)
				}
				if _, errServe := restarted.Execute(ctx, []string{tc.provider}, request, cliproxyexecutor.Options{}); errServe != nil {
					t.Fatalf("request after restart failed: %v", errServe)
				}
				if len(restartedExec.served) != 1 {
					t.Fatalf("restarted manager served %v", restartedExec.served)
				}
			})
		}
	}
}

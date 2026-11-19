package auth

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/MoeForge/moego/json"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executionregistry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// recordingCatalogueHomeDispatcher offers one catalogue and records the model
// each dispatch asks Home for.
type recordingCatalogueHomeDispatcher struct {
	catalogue string
	mu        sync.Mutex
	requested []string
}

func (d *recordingCatalogueHomeDispatcher) HeartbeatOK() bool { return true }

func (d *recordingCatalogueHomeDispatcher) RPopAuth(_ context.Context, requestedModel string, _ string, _ http.Header, _ int) ([]byte, error) {
	d.mu.Lock()
	d.requested = append(d.requested, requestedModel)
	d.mu.Unlock()
	return json.Marshal(homeAuthDispatchResponse{Auth: Auth{ID: "home-auth", Provider: "home-execution", Status: StatusActive}})
}

func (d *recordingCatalogueHomeDispatcher) AbortAmbiguousDispatch() {}

func (d *recordingCatalogueHomeDispatcher) GetModels(context.Context, http.Header, url.Values) ([]byte, error) {
	return []byte(d.catalogue), nil
}

// A model the Home model lists show under its own name, because Home offers
// it only under an effort-named id, is dispatched to Home under that id.
func TestHomeExecutionDispatchesAListedOwnName(t *testing.T) {
	dispatcher := &recordingCatalogueHomeDispatcher{catalogue: `{"antigravity":[
		{"id":"claude-opus-5-5-high","thinking":{"levels":["low","medium","high"]}}
	]}`}
	manager := NewManager(nil, nil, nil)
	manager.SetConfig(&internalconfig.Config{Home: internalconfig.HomeConfig{Enabled: true}})
	manager.PublishHomeDispatch(dispatcher, executionregistry.New(), 1)
	manager.RegisterExecutor(&homeExecutionExecutor{})

	if _, errExecute := manager.Execute(context.Background(), []string{"home-execution"}, cliproxyexecutor.Request{Model: "claude-opus-5-5"}, cliproxyexecutor.Options{}); errExecute != nil {
		t.Fatalf("Execute() error = %v", errExecute)
	}
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if len(dispatcher.requested) == 0 || dispatcher.requested[0] != "claude-opus-5-5-high" {
		t.Fatalf("Home was asked for %v, want claude-opus-5-5-high", dispatcher.requested)
	}
}

package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/home"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// catalogueHomeDispatcher is a Home client that offers one model catalogue.
type catalogueHomeDispatcher struct {
	catalogue string
	err       error
}

func (d *catalogueHomeDispatcher) HeartbeatOK() bool { return true }

func (d *catalogueHomeDispatcher) RPopAuth(context.Context, string, string, http.Header, int) ([]byte, error) {
	return nil, home.ErrAuthNotFound
}

func (d *catalogueHomeDispatcher) AbortAmbiguousDispatch() {}

func (d *catalogueHomeDispatcher) GetModels(context.Context, http.Header, url.Values) ([]byte, error) {
	return []byte(d.catalogue), d.err
}

func TestHomeDispatchModelRequestsTheCatalogueIDThatServesAModelsOwnName(t *testing.T) {
	const effortNamedOnly = `{"antigravity":[
		{"id":"claude-opus-5-5-high","thinking":{"levels":["low","medium","high"]}},
		{"id":"gemini-4-hydrogen-low","thinking":{"levels":["low"]}},
		{"id":"gemini-4-hydrogen-xhigh","thinking":{"levels":["xhigh"]}}
	]}`
	const servedUnderOwnName = `{
		"antigravity":[{"id":"claude-opus-5-5-high","thinking":{"levels":["low","medium","high"]}}],
		"claude":[{"id":"claude-opus-5-5","thinking":{"levels":["low","medium","high"]}}]
	}`
	cases := []struct {
		name       string
		dispatcher *catalogueHomeDispatcher
		routeModel string
		request    string
		want       string
	}{
		{
			name:       "offered only under an effort-named id",
			dispatcher: &catalogueHomeDispatcher{catalogue: effortNamedOnly},
			routeModel: "claude-opus-5-5",
			want:       "claude-opus-5-5-high",
		},
		{
			name:       "the id named for the requested effort",
			dispatcher: &catalogueHomeDispatcher{catalogue: effortNamedOnly},
			routeModel: "gemini-4-hydrogen",
			request:    `{"model":"gemini-4-hydrogen","reasoning_effort":"low"}`,
			want:       "gemini-4-hydrogen-low",
		},
		{
			name:       "thinking suffix carried over",
			dispatcher: &catalogueHomeDispatcher{catalogue: effortNamedOnly},
			routeModel: "claude-opus-5-5(low)",
			want:       "claude-opus-5-5-high(low)",
		},
		{
			name:       "effort-named id requested as given",
			dispatcher: &catalogueHomeDispatcher{catalogue: effortNamedOnly},
			routeModel: "claude-opus-5-5-high",
			want:       "claude-opus-5-5-high",
		},
		{
			name:       "served under its own name by another provider",
			dispatcher: &catalogueHomeDispatcher{catalogue: servedUnderOwnName},
			routeModel: "claude-opus-5-5",
			want:       "claude-opus-5-5",
		},
		{
			name:       "catalogue request fails",
			dispatcher: &catalogueHomeDispatcher{err: errors.New("home unavailable")},
			routeModel: "claude-opus-5-5",
			want:       "claude-opus-5-5",
		},
		{
			name:       "Home answers with an error envelope",
			dispatcher: &catalogueHomeDispatcher{catalogue: `{"error":{"type":"invalid_credential","message":"unknown key"}}`},
			routeModel: "claude-opus-5-5",
			want:       "claude-opus-5-5",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := cliproxyexecutor.Options{
				SourceFormat:    sdktranslator.FormatOpenAI,
				OriginalRequest: []byte(tc.request),
			}
			got := homeDispatchModel(context.Background(), tc.dispatcher, tc.routeModel, http.Header{}, opts)
			if got != tc.want {
				t.Fatalf("homeDispatchModel(%q) = %q, want %q", tc.routeModel, got, tc.want)
			}
		})
	}
}

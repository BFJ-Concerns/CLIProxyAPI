package auth

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/MoeForge/moego/json"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	log "github.com/sirupsen/logrus"
)

// homeModelCatalogueSource is a Home client that can report the catalogue
// Home offers the caller its headers identify.
type homeModelCatalogueSource interface {
	GetModels(ctx context.Context, headers http.Header, query url.Values) ([]byte, error)
}

// homeDispatchModel returns the id to request of Home for routeModel.
//
// A model that Home's catalogue offers only under ids named for single
// reasoning efforts, as "claude-opus-5-5" offered as "claude-opus-5-5-high",
// is requested under the id registry.EffortVariantFor picks for the effort
// the request asks for, and a thinking suffix on routeModel is carried over
// to that id. A model that some Home route serves under its own name is
// returned unchanged, so Home keeps routing it across every provider that
// offers it. routeModel is also returned unchanged when client cannot report
// a catalogue or the catalogue cannot be read: Home then answers for the
// name as it was given.
//
// Only the model named in the dispatch request changes. The caller's route
// model still keys session affinity and retained session selections, and
// execution uses the model Home returns with the credential.
//
// Home is asked for the catalogue on every call. A catalogue remembered
// between calls belongs behind readHomeModelCatalogue, kept apart for each
// caller, because Home offers each caller its own catalogue.
func homeDispatchModel(ctx context.Context, client homeAuthDispatcher, routeModel string, headers http.Header, opts cliproxyexecutor.Options) string {
	catalogue, ok := readHomeModelCatalogue(ctx, client, headers)
	if !ok {
		return routeModel
	}
	suffix := thinking.ParseSuffix(routeModel)
	effort := thinking.ExtractReasoningEffort(opts.OriginalRequest, opts.SourceFormat.String(), routeModel)
	variant, ok := registry.EffortVariantFor(catalogue, suffix.ModelName, effort)
	if !ok {
		return routeModel
	}
	if suffix.HasSuffix {
		return variant + "(" + suffix.RawSuffix + ")"
	}
	return variant
}

// readHomeModelCatalogue asks Home for the catalogue it offers the caller
// that headers identify. It reports false when client is not a catalogue
// source, the request fails, or Home answers with anything but a catalogue.
func readHomeModelCatalogue(ctx context.Context, client homeAuthDispatcher, headers http.Header) ([]*registry.ModelInfo, bool) {
	source, ok := client.(homeModelCatalogueSource)
	if !ok {
		return nil, false
	}
	raw, errGet := source.GetModels(ctx, headers, nil)
	if errGet != nil {
		log.Debugf("Home model catalogue request failed: %v", errGet)
		return nil, false
	}
	catalogue, errDecode := decodeHomeModelCatalogue(raw)
	if errDecode != nil {
		log.Debugf("Home model catalogue decode failed: %v", errDecode)
		return nil, false
	}
	return catalogue, true
}

// decodeHomeModelCatalogue reads the ids and thinking support out of Home's
// model catalogue, which lists each provider's models under that provider's
// name. An id that several providers offer appears once for each of them. An
// error envelope is not a catalogue and fails to decode.
func decodeHomeModelCatalogue(raw []byte) ([]*registry.ModelInfo, error) {
	var byProvider map[string][]struct {
		ID       string                    `json:"id"`
		Name     string                    `json:"name"`
		Thinking *registry.ThinkingSupport `json:"thinking"`
	}
	if errUnmarshal := json.Unmarshal(raw, &byProvider); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	var catalogue []*registry.ModelInfo
	for _, models := range byProvider {
		for _, model := range models {
			id := strings.TrimSpace(model.ID)
			if id == "" {
				id = strings.TrimPrefix(strings.TrimSpace(model.Name), "models/")
			}
			if id == "" {
				continue
			}
			catalogue = append(catalogue, &registry.ModelInfo{ID: id, Thinking: model.Thinking})
		}
	}
	return catalogue, nil
}

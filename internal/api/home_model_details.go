package api

import "github.com/router-for-me/CLIProxyAPI/v8/internal/registry"

// addHomeModelListDetails adds the description fields a model list carries
// to model, an entry built from the Home control plane's catalogue for
// handlerType. Home may leave a model's thinking unstated, so an entry
// without it is not described as reasoning or not. A model Home routes to
// several providers accepts every effort any of them accepts.
func addHomeModelListDetails(model map[string]any, entry homeModelEntry, handlerType string) {
	supports := []*registry.ThinkingSupport{entry.thinking}
	for _, route := range entry.routes {
		supports = append(supports, route.thinking)
	}
	registry.AddModelListDetails(model, &registry.ModelInfo{
		ContextLength:             entry.contextLength,
		MaxCompletionTokens:       entry.maxCompletionTokens,
		Thinking:                  registry.CombineThinkingSupport(supports),
		SupportedInputModalities:  entry.supportedInputModalities,
		SupportedOutputModalities: entry.supportedOutputModalities,
	}, handlerType, true)
}

package api

import "github.com/router-for-me/CLIProxyAPI/v8/internal/registry"

// addHomeModelListDetails adds the description fields a model list carries
// to model, an entry built from the Home control plane's catalogue for
// handlerType. Home may leave a model's thinking unstated, so an entry
// without it is not described as reasoning or not.
func addHomeModelListDetails(model map[string]any, entry homeModelEntry, handlerType string) {
	registry.AddModelListDetails(model, &registry.ModelInfo{
		ContextLength:             entry.contextLength,
		MaxCompletionTokens:       entry.maxCompletionTokens,
		Thinking:                  entry.thinking,
		SupportedInputModalities:  entry.supportedInputModalities,
		SupportedOutputModalities: entry.supportedOutputModalities,
	}, handlerType, true)
}

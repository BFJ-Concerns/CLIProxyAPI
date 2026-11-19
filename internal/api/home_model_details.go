package api

import (
	"sort"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

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

// foldHomeEffortVariants gives each model that Home's catalogue offers only
// under ids named for single reasoning efforts one entry under the model's
// own name, accepting every effort its variants accept, and leaves the
// effort-named ids out, as registry.FoldEffortVariants does for the local
// registry. Home dispatch maps the own name back to the id that serves the
// requested effort; a model Home also serves under its own name keeps that
// entry as it is, since Home dispatches the name to it unchanged.
func foldHomeEffortVariants(entries []homeModelEntry) []homeModelEntry {
	infos := make([]*registry.ModelInfo, 0, len(entries))
	byID := make(map[string]homeModelEntry, len(entries))
	for _, entry := range entries {
		supports := []*registry.ThinkingSupport{entry.thinking}
		for _, route := range entry.routes {
			supports = append(supports, route.thinking)
		}
		infos = append(infos, &registry.ModelInfo{
			ID:                        entry.id,
			DisplayName:               entry.displayName,
			Description:               entry.description,
			ContextLength:             entry.contextLength,
			MaxCompletionTokens:       entry.maxCompletionTokens,
			Thinking:                  registry.CombineThinkingSupport(supports),
			SupportedInputModalities:  entry.supportedInputModalities,
			SupportedOutputModalities: entry.supportedOutputModalities,
		})
		byID[entry.id] = entry
	}
	folded := registry.FoldEffortVariants(infos)
	variantsOf := make(map[string][]homeModelEntry)
	for _, info := range folded {
		if info.EffortVariantOf != "" {
			variantsOf[info.EffortVariantOf] = append(variantsOf[info.EffortVariantOf], byID[info.ID])
		}
	}
	if len(variantsOf) == 0 {
		return entries
	}
	out := make([]homeModelEntry, 0, len(folded))
	for _, info := range folded {
		if info.EffortVariantOf != "" {
			continue
		}
		variants := variantsOf[info.ID]
		entry, own := byID[info.ID]
		if !own {
			entry = homeModelEntry{
				id:                        info.ID,
				modelName:                 info.ID,
				created:                   variants[0].created,
				ownedBy:                   variants[0].ownedBy,
				modelType:                 variants[0].modelType,
				displayName:               info.DisplayName,
				description:               info.Description,
				contextLength:             info.ContextLength,
				maxCompletionTokens:       info.MaxCompletionTokens,
				supportedInputModalities:  info.SupportedInputModalities,
				supportedOutputModalities: info.SupportedOutputModalities,
			}
			for _, variant := range variants {
				for _, provider := range variant.providers {
					entry.providers = appendUniqueHomeProvider(entry.providers, provider)
				}
			}
			entry.thinking = info.Thinking
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

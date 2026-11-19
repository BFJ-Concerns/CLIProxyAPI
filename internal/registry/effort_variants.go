package registry

import (
	"strings"
)

// effortOrder ranks the reasoning efforts a catalogue id can be named for,
// lowest first. "none" is absent: an id named for no reasoning is a distinct
// way of running the model, not one of its efforts.
var effortOrder = []string{"minimal", "low", "medium", "high", "xhigh", "max"}

func effortRank(effort string) int {
	effort = strings.ToLower(strings.TrimSpace(effort))
	for i, candidate := range effortOrder {
		if candidate == effort {
			return i
		}
	}
	return -1
}

// splitEffortSuffix splits a catalogue id named for one reasoning effort, such
// as "gemini-4-hydrogen-xhigh", into the model's own name and the effort.
func splitEffortSuffix(id string) (base, effort string, ok bool) {
	lower := strings.ToLower(id)
	for _, candidate := range effortOrder {
		suffix := "-" + candidate
		if strings.HasSuffix(lower, suffix) && len(id) > len(suffix) {
			return id[:len(id)-len(suffix)], candidate, true
		}
	}
	return id, "", false
}

// isEffortVariant reports whether info is a catalogue entry named for one
// reasoning effort of a model that reasons.
func isEffortVariant(info *ModelInfo) (base, effort string, ok bool) {
	if info == nil || info.Thinking == nil {
		return "", "", false
	}
	return splitEffortSuffix(strings.TrimSpace(info.ID))
}

// FoldEffortVariants gives each model that a catalogue names for single
// reasoning efforts one entry under the model's own name. That entry accepts
// every effort its variants accept, and is placed where the first variant
// was. Where the catalogue also lists the model under its own name, that
// entry stands as it is: requests for the name are served by it. The variants stay in the result, marked with EffortVariantOf, so that
// requests naming them keep working while model lists leave them out.
// Entries with no thinking support are left alone: their name is the only
// evidence of an effort, and the proxy would not apply one.
func FoldEffortVariants(models []*ModelInfo) []*ModelInfo {
	return foldEffortVariantsWhere(models, nil)
}

// FoldEffortVariantsUnless folds as FoldEffortVariants does, except where
// excluded reports the model's own name: those variants are left as they
// are, listed under their own ids, since nothing stands for them.
func FoldEffortVariantsUnless(models []*ModelInfo, excluded func(ownName string) bool) []*ModelInfo {
	return foldEffortVariantsWhere(models, excluded)
}

func foldEffortVariantsWhere(models []*ModelInfo, excluded func(ownName string) bool) []*ModelInfo {
	foldableVariant := func(model *ModelInfo) (string, string, bool) {
		base, effort, ok := isEffortVariant(model)
		if !ok || (excluded != nil && excluded(base)) {
			return "", "", false
		}
		return base, effort, true
	}
	type group struct {
		variants []*ModelInfo
		efforts  []string
	}
	groups := make(map[string]*group)
	existing := make(map[string]*ModelInfo)
	for _, model := range models {
		if model == nil {
			continue
		}
		existing[strings.ToLower(strings.TrimSpace(model.ID))] = model
	}
	for _, model := range models {
		base, effort, ok := foldableVariant(model)
		if !ok {
			continue
		}
		key := strings.ToLower(base)
		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
		}
		g.variants = append(g.variants, model)
		g.efforts = append(g.efforts, effort)
	}
	if len(groups) == 0 {
		return models
	}

	out := make([]*ModelInfo, 0, len(models)+len(groups))
	placed := make(map[string]bool, len(groups))
	for _, model := range models {
		if model == nil {
			out = append(out, model)
			continue
		}
		key := strings.ToLower(strings.TrimSpace(model.ID))
		if _, isBase := groups[key]; isBase && !placed[key] {
			// The catalogue already lists the model under its own name, and a
			// request for that name is served by that entry, so it keeps the
			// efforts it accepts.
			placed[key] = true
			out = append(out, model)
			continue
		}
		base, _, ok := foldableVariant(model)
		if !ok {
			out = append(out, model)
			continue
		}
		baseKey := strings.ToLower(base)
		g := groups[baseKey]
		if !placed[baseKey] {
			if _, listedLater := existing[baseKey]; !listedLater {
				placed[baseKey] = true
				out = append(out, foldedEntry(g.variants, g.efforts))
			}
		}
		variant := cloneModelInfo(model)
		variant.EffortVariantOf = base
		out = append(out, variant)
	}
	return out
}

// foldedEntry builds the single entry for a model from its effort variants,
// starting from the variant named for the highest effort.
func foldedEntry(variants []*ModelInfo, efforts []string) *ModelInfo {
	representative := 0
	for i := range variants {
		if effortRank(efforts[i]) > effortRank(efforts[representative]) {
			representative = i
		}
	}
	source := variants[representative]
	base, _, _ := splitEffortSuffix(strings.TrimSpace(source.ID))
	entry := cloneModelInfo(source)
	if strings.EqualFold(strings.TrimSpace(entry.Name), strings.TrimSpace(entry.ID)) {
		entry.Name = base
	}
	entry.ID = base
	entry.DisplayName = trimEffortLabel(entry.DisplayName, efforts[representative])
	entry.Description = trimEffortLabel(entry.Description, efforts[representative])

	accepted := make(map[string]bool)
	var thinking ThinkingSupport
	if entry.Thinking != nil {
		thinking = *entry.Thinking
		for _, level := range entry.Thinking.Levels {
			accepted[strings.ToLower(level)] = true
		}
	}
	for i, variant := range variants {
		accepted[efforts[i]] = true
		support := variant.Thinking
		for _, level := range support.Levels {
			accepted[strings.ToLower(level)] = true
		}
		if support.Min > 0 && (thinking.Min == 0 || support.Min < thinking.Min) {
			thinking.Min = support.Min
		}
		if support.Max > thinking.Max {
			thinking.Max = support.Max
		}
		thinking.ZeroAllowed = thinking.ZeroAllowed || support.ZeroAllowed
		thinking.DynamicAllowed = thinking.DynamicAllowed || support.DynamicAllowed
		if thinking.DisableType == "" {
			thinking.DisableType = support.DisableType
		}
	}
	thinking.Levels = orderedLevels(accepted)
	entry.Thinking = &thinking
	entry.EffortVariantOf = ""
	entry.EffortFolded = true
	return entry
}

// orderedLevels lists accepted levels in the thinking pipeline's order,
// keeping any level this package does not rank after the ranked ones.
func orderedLevels(accepted map[string]bool) []string {
	levels := make([]string, 0, len(accepted))
	if accepted["none"] {
		levels = append(levels, "none")
	}
	for _, effort := range effortOrder {
		if accepted[effort] {
			levels = append(levels, effort)
		}
	}
	for level := range accepted {
		if level != "none" && effortRank(level) < 0 {
			levels = append(levels, level)
		}
	}
	return levels
}

// trimEffortLabel removes a trailing "(High)"-style effort label from a
// display name or description.
func trimEffortLabel(text, effort string) string {
	trimmed := strings.TrimSpace(text)
	label := "(" + effort + ")"
	if len(trimmed) >= len(label) && strings.EqualFold(trimmed[len(trimmed)-len(label):], label) {
		return strings.TrimSpace(trimmed[:len(trimmed)-len(label)])
	}
	return trimmed
}

// EffortVariantFor returns the catalogue id that serves model at effort when
// model is the folded name of effort-named entries in catalogue, and false
// when catalogue lists model under its own name or has no variants of it.
// It prefers the variant named for effort, then the highest-effort variant
// that accepts effort, then the highest-effort variant.
func EffortVariantFor(catalogue []*ModelInfo, model, effort string) (string, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", false
	}
	var variants []*ModelInfo
	for _, info := range catalogue {
		if info == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(info.ID), model) {
			return "", false
		}
		if base, _, ok := isEffortVariant(info); ok && strings.EqualFold(base, model) {
			variants = append(variants, info)
		}
	}
	if chosen := chooseEffortVariant(variants, effort); chosen != nil {
		return strings.TrimSpace(chosen.ID), true
	}
	return "", false
}

// RegisteredEffortVariantFor returns the upstream id that serves model at
// effort when model is an entry FoldEffortVariants made in registered, a
// credential's registered models, and false for any other model. It chooses
// among the variants the credential registered as EffortVariantFor does, so
// a variant the operator excluded is never chosen, and it returns the
// catalogue id a variant renamed by an alias was made from.
func RegisteredEffortVariantFor(registered []*ModelInfo, model, effort string) (string, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", false
	}
	folded := false
	var variants []*ModelInfo
	for _, info := range registered {
		if info == nil {
			continue
		}
		id := strings.TrimSpace(info.ID)
		if strings.EqualFold(id, model) {
			folded = info.EffortFolded
			continue
		}
		// A prefixed copy of a variant keeps the unprefixed EffortVariantOf;
		// only the variant registered under its own id stands for the model.
		if base, _, ok := splitEffortSuffix(id); ok && strings.EqualFold(base, model) && strings.EqualFold(info.EffortVariantOf, model) {
			variants = append(variants, info)
		}
	}
	if !folded {
		return "", false
	}
	chosen := chooseEffortVariant(variants, effort)
	if chosen == nil {
		return "", false
	}
	// An alias registers a copy of the catalogue entry under the alias and
	// records the catalogue id as its MetadataModelID.
	if upstream := strings.TrimSpace(chosen.MetadataModelID); upstream != "" {
		return upstream, true
	}
	return strings.TrimSpace(chosen.ID), true
}

// chooseEffortVariant picks, from effort-named entries of one model, the one
// named for effort, then the highest-effort one that accepts effort, then the
// highest-effort one.
func chooseEffortVariant(variants []*ModelInfo, effort string) *ModelInfo {
	effort = strings.ToLower(strings.TrimSpace(effort))
	var best, accepting *ModelInfo
	bestRank, acceptingRank := -1, -1
	for _, info := range variants {
		_, variantEffort, _ := splitEffortSuffix(strings.TrimSpace(info.ID))
		if effort != "" && variantEffort == effort {
			return info
		}
		rank := effortRank(variantEffort)
		if rank > bestRank {
			best, bestRank = info, rank
		}
		if effort != "" && rank > acceptingRank && thinkingAcceptsLevel(info.Thinking, effort) {
			accepting, acceptingRank = info, rank
		}
	}
	if accepting != nil {
		return accepting
	}
	return best
}

func thinkingAcceptsLevel(support *ThinkingSupport, level string) bool {
	if support == nil {
		return false
	}
	for _, candidate := range support.Levels {
		if strings.EqualFold(candidate, level) {
			return true
		}
	}
	return false
}

// registrationListed reports whether a model list shows the registration: it
// is left out only when every provider registered it as an effort variant of
// a model listed under its own name.
func registrationListed(registration *ModelRegistration) bool {
	if registration == nil || registration.Info == nil || registration.Info.EffortVariantOf == "" {
		return true
	}
	for _, info := range registration.InfoByProvider {
		if info != nil && info.EffortVariantOf == "" {
			return true
		}
	}
	return false
}

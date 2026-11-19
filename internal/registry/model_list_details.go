package registry

import "strings"

// Model-list entries describe what a client needs to choose and drive each
// model: its token limits, whether and how it reasons, and its modalities.
// A field is left out when the registry holds no value for it.

// addModelListDetails adds the description fields to entry, a model-list
// entry already built for handlerType ("openai", "claude" or "gemini") from a
// registered model.
func addModelListDetails(entry map[string]any, info *ModelInfo, handlerType string) {
	if info == nil {
		return
	}
	AddModelListDetails(entry, info, handlerType, info.UserDefined)
}

// AddModelListDetails adds the description fields of info to entry, a
// model-list entry built for handlerType ("openai", "claude" or "gemini").
// unsetThinkingIsUnknown says what a nil info.Thinking means: true when the
// source may simply not have said, so reasoning is left out; false when the
// proxy treats the model as not reasoning, so it is described that way.
func AddModelListDetails(entry map[string]any, info *ModelInfo, handlerType string, unsetThinkingIsUnknown bool) {
	if entry == nil || info == nil {
		return
	}
	switch handlerType {
	case "openai":
		setPositiveInt(entry, "context_length", info.ContextLength)
		setPositiveInt(entry, "max_completion_tokens", info.MaxCompletionTokens)
		setPositiveInt(entry, "input_token_limit", info.InputTokenLimit)
		setPositiveInt(entry, "output_token_limit", info.OutputTokenLimit)
		setStringList(entry, "input_modalities", info.SupportedInputModalities)
		setStringList(entry, "output_modalities", info.SupportedOutputModalities)
		if reasoning := reasoningDescription(info, snakeCaseReasoningKeys, unsetThinkingIsUnknown); reasoning != nil {
			entry["reasoning"] = reasoning
		}
	case "claude":
		// The Anthropic entry's token limits fall back to defaults when the
		// model has none; an unknown limit is left out instead.
		if info.ContextLength <= 0 {
			delete(entry, "max_input_tokens")
		}
		if info.MaxCompletionTokens <= 0 {
			delete(entry, "max_tokens")
		}
		setPositiveInt(entry, "context_length", info.ContextLength)
		setStringList(entry, "input_modalities", info.SupportedInputModalities)
		setStringList(entry, "output_modalities", info.SupportedOutputModalities)
		reasoning := reasoningDescription(info, snakeCaseReasoningKeys, unsetThinkingIsUnknown)
		if reasoning != nil {
			entry["reasoning"] = reasoning
		}
		if capabilities := anthropicCapabilities(info, reasoning); len(capabilities) > 0 {
			entry["capabilities"] = capabilities
		}
	case "gemini":
		if reasoning := reasoningDescription(info, camelCaseReasoningKeys, unsetThinkingIsUnknown); reasoning != nil {
			entry["reasoning"] = reasoning
			entry["thinking"] = reasoning["supported"]
		}
	}
}

type reasoningKeys struct {
	efforts    string
	budget     string
	canDisable string
	dynamic    string
}

var (
	snakeCaseReasoningKeys = reasoningKeys{efforts: "efforts", budget: "budget_tokens", canDisable: "can_disable", dynamic: "dynamic_budget"}
	camelCaseReasoningKeys = reasoningKeys{efforts: "efforts", budget: "budgetTokens", canDisable: "canDisable", dynamic: "dynamicBudget"}
)

// reasoningDescription describes the model's reasoning as the proxy applies
// it. A catalogue model without thinking support has its reasoning settings
// stripped, so it is described as not reasoning; where a nil Thinking means
// nothing is known (a user-defined model's settings pass through untouched),
// nil is returned.
func reasoningDescription(info *ModelInfo, keys reasoningKeys, unsetThinkingIsUnknown bool) map[string]any {
	support := info.Thinking
	if support == nil {
		if unsetThinkingIsUnknown {
			return nil
		}
		return map[string]any{"supported": false}
	}
	reasoning := map[string]any{"supported": true}
	canDisable := support.ZeroAllowed
	efforts := make([]string, 0, len(support.Levels))
	for _, level := range support.Levels {
		if level == "none" {
			canDisable = true
		}
		efforts = append(efforts, level)
	}
	if len(efforts) > 0 {
		reasoning[keys.efforts] = efforts
	}
	if support.Max > 0 {
		reasoning[keys.budget] = map[string]any{"min": support.Min, "max": support.Max}
	}
	reasoning[keys.canDisable] = canDisable
	if support.DynamicAllowed {
		reasoning[keys.dynamic] = true
	}
	return reasoning
}

// anthropicCapabilities fills the capability objects of Anthropic's model
// list that the registry can answer: thinking, effort and image input.
func anthropicCapabilities(info *ModelInfo, reasoning map[string]any) map[string]any {
	capabilities := map[string]any{}
	if reasoning != nil {
		supported, _ := reasoning["supported"].(bool)
		capabilities["thinking"] = map[string]any{"supported": supported}
		effort := map[string]any{"supported": false}
		if efforts, ok := reasoning[snakeCaseReasoningKeys.efforts].([]string); ok {
			for _, level := range efforts {
				if level == "none" {
					continue
				}
				effort["supported"] = true
				effort[level] = map[string]any{"supported": true}
			}
		}
		capabilities["effort"] = effort
	}
	if len(info.SupportedInputModalities) > 0 {
		capabilities["image_input"] = map[string]any{"supported": containsFold(info.SupportedInputModalities, "image")}
	}
	return capabilities
}

func setPositiveInt(entry map[string]any, key string, value int) {
	if value > 0 {
		entry[key] = value
	}
}

func setStringList(entry map[string]any, key string, values []string) {
	if len(values) > 0 {
		entry[key] = append([]string(nil), values...)
	}
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

// listedModelInfo is the model a list entry describes. A model several
// providers serve accepts every effort any of them accepts, so the thinking
// support of every provider still serving it is combined.
func listedModelInfo(registration *ModelRegistration) *ModelInfo {
	if registration == nil || registration.Info == nil {
		return nil
	}
	info := registration.Info
	var supports []*ThinkingSupport
	userDefined := info.UserDefined
	if info.Thinking != nil {
		supports = append(supports, info.Thinking)
	}
	for provider, providerInfo := range registration.InfoByProvider {
		if providerInfo == nil || providerInfo == info || registration.Providers[provider] <= 0 {
			continue
		}
		if providerInfo.Thinking != nil {
			supports = append(supports, providerInfo.Thinking)
		}
	}
	if len(supports) <= 1 && (info.Thinking != nil || len(supports) == 0) {
		return info
	}
	combined := cloneModelInfo(info)
	combined.UserDefined = userDefined
	merged := ThinkingSupport{}
	accepted := make(map[string]bool)
	for _, support := range supports {
		for _, level := range support.Levels {
			accepted[strings.ToLower(level)] = true
		}
		if support.Min > 0 && (merged.Min == 0 || support.Min < merged.Min) {
			merged.Min = support.Min
		}
		if support.Max > merged.Max {
			merged.Max = support.Max
		}
		merged.ZeroAllowed = merged.ZeroAllowed || support.ZeroAllowed
		merged.DynamicAllowed = merged.DynamicAllowed || support.DynamicAllowed
	}
	if len(accepted) > 0 {
		merged.Levels = orderedLevels(accepted)
	}
	combined.Thinking = &merged
	return combined
}

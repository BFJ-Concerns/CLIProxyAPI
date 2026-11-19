package openai

// modelListDetailKeys are the registry's description fields that /v1/models
// keeps in its OpenAI-shaped entries beside id, object, created and owned_by.
var modelListDetailKeys = []string{
	"context_length",
	"max_completion_tokens",
	"input_token_limit",
	"output_token_limit",
	"reasoning",
	"input_modalities",
	"output_modalities",
}

// copyModelListDetails copies the description fields present in model into
// entry.
func copyModelListDetails(entry, model map[string]any) {
	for _, key := range modelListDetailKeys {
		if value, ok := model[key]; ok {
			entry[key] = value
		}
	}
}

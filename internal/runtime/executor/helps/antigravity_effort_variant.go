package helps

import (
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
)

// ResolveAntigravityEffortVariant maps a model listed under its own name to
// the Antigravity catalogue id that serves it at the requested effort, as
// "gemini-4-hydrogen" to "gemini-4-hydrogen-xhigh". The effort comes from the
// model's thinking suffix or the request body in sourceFormat; a thinking
// suffix is carried over to the returned id. Any other model is returned
// unchanged.
func ResolveAntigravityEffortVariant(model string, payload []byte, sourceFormat string) string {
	suffix := thinking.ParseSuffix(model)
	effort := thinking.ExtractReasoningEffort(payload, sourceFormat, model)
	variant, ok := registry.EffortVariantFor(registry.GetAntigravityModels(), suffix.ModelName, effort)
	if !ok {
		return model
	}
	if suffix.HasSuffix {
		return variant + "(" + suffix.RawSuffix + ")"
	}
	return variant
}

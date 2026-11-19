package helps

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// ResolveAntigravityEffortVariant maps a folded model's own name to the
// Antigravity catalogue id that serves the requested effort, such as
// "gemini-4-hydrogen-xhigh" for "gemini-4-hydrogen". It honours the
// credential's registered models, including exclusions, aliases and prefixes.
// For credentials without registered models, such as those dispatched by Home,
// it uses the static catalogue. The effort comes from the model's thinking
// suffix or the request body in sourceFormat. The returned id retains any
// thinking suffix. Any other model is returned unchanged.
func ResolveAntigravityEffortVariant(auth *cliproxyauth.Auth, model string, payload []byte, sourceFormat string) string {
	if auth == nil {
		return model
	}
	suffix := thinking.ParseSuffix(model)
	registered := registry.GetGlobalRegistry().GetModelsForClient(auth.ID)
	effort := thinking.ExtractReasoningEffort(payload, sourceFormat, model)
	var variant string
	var ok bool
	if len(registered) == 0 {
		variant, ok = registry.EffortVariantFor(registry.GetAntigravityModels(), suffix.ModelName, effort)
	} else {
		variant, ok = registry.RegisteredEffortVariantFor(registered, suffix.ModelName, effort)
		// The conductor strips the credential's prefix before the executor
		// runs, and with force-model-prefix the credential registered only the
		// prefixed names.
		if prefix := strings.TrimSpace(auth.Prefix); !ok && prefix != "" {
			variant, ok = registry.RegisteredEffortVariantFor(registered, prefix+"/"+suffix.ModelName, effort)
		}
	}
	if !ok {
		return model
	}
	if suffix.HasSuffix {
		return variant + "(" + suffix.RawSuffix + ")"
	}
	return variant
}

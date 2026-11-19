package helps

import "testing"

func TestResolveAntigravityEffortVariantMapsTheModelsOwnNameToItsCatalogueID(t *testing.T) {
	cases := []struct {
		name, model, payload, want string
	}{
		{"own name", "claude-opus-5-5", `{}`, "claude-opus-5-5-high"},
		{"own name with suffix", "gemini-4-hydrogen(low)", `{}`, "gemini-4-hydrogen-xhigh(low)"},
		{"own name with body effort", "gemini-4-argon", `{"reasoning_effort":"medium"}`, "gemini-4-argon-high"},
		{"effort-named id", "claude-opus-5-5-high(low)", `{}`, "claude-opus-5-5-high(low)"},
		{"catalogue id", "gemini-3-flash", `{}`, "gemini-3-flash"},
		{"unknown", "not-a-model", `{}`, "not-a-model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveAntigravityEffortVariant(tc.model, []byte(tc.payload), "openai"); got != tc.want {
				t.Fatalf("ResolveAntigravityEffortVariant(%q) = %q, want %q", tc.model, got, tc.want)
			}
		})
	}
}

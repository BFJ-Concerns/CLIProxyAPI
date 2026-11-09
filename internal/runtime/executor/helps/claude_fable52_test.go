package helps

import "testing"

func TestIsClaudeFable52Model(t *testing.T) {
	for _, tt := range []struct {
		model             string
		wantGeneration    bool
		wantFableOnlyFlag bool
	}{
		{model: "claude-fable-5-2", wantGeneration: true, wantFableOnlyFlag: true},
		{model: "Claude-Fable-5-2[1m]", wantGeneration: true, wantFableOnlyFlag: true},
		{model: "us.anthropic.claude-fable-5-2", wantGeneration: true, wantFableOnlyFlag: true},
		{model: "claude-fable-5.2", wantGeneration: true, wantFableOnlyFlag: true},
		{model: "claude-mythos-5-2", wantGeneration: true},
		{model: "claude-fable-5-1"},
		{model: "claude-fable-5"},
		{model: "claude-fable-5-20"},
		{model: "claude-opus-5-5"},
		{model: ""},
	} {
		t.Run(tt.model, func(t *testing.T) {
			if got := IsClaudeFable52Model(tt.model); got != tt.wantGeneration {
				t.Errorf("IsClaudeFable52Model(%q) = %t, want %t", tt.model, got, tt.wantGeneration)
			}
			if got := IsClaudeFable52OnlyModel(tt.model); got != tt.wantFableOnlyFlag {
				t.Errorf("IsClaudeFable52OnlyModel(%q) = %t, want %t", tt.model, got, tt.wantFableOnlyFlag)
			}
		})
	}
}

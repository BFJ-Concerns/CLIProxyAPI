package helps

import "testing"

func TestIsClaudeMythos52Model(t *testing.T) {
	for _, tt := range []struct {
		model string
		want  bool
	}{
		{model: "claude-mythos-5-2", want: true},
		{model: "Claude-Mythos-5-2[1m]", want: true},
		{model: "us.anthropic.claude-mythos-5-2", want: true},
		{model: "claude-mythos-5.2", want: true},
		{model: "claude-mythos-5-1"},
		{model: "claude-mythos-5-20"},
		{model: "claude-fable-5-2"},
		{model: ""},
	} {
		t.Run(tt.model, func(t *testing.T) {
			if got := IsClaudeMythos52Model(tt.model); got != tt.want {
				t.Errorf("IsClaudeMythos52Model(%q) = %t, want %t", tt.model, got, tt.want)
			}
		})
	}
}

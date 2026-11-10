package helps

// claudeCodeXAppValues are the X-App values Claude Code itself sends.
// Interactive and print-mode sessions send "cli"; background sessions started
// with `claude --bg` send "cli-bg" with an otherwise identical request shape
// (same "(external, cli)" User-Agent, claude-code beta, metadata.user_id and
// billing entrypoint). A confirmed client forwards its own value upstream.
var claudeCodeXAppValues = map[string]bool{
	"cli":    true,
	"cli-bg": true,
}

// isClaudeCodeXApp reports whether value is an X-App header Claude Code sends.
func isClaudeCodeXApp(value string) bool {
	return claudeCodeXAppValues[value]
}

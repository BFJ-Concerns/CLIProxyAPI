package helps

import "testing"

func TestDetectClaudeCodeRequestConfirmsBackgroundSession(t *testing.T) {
	headers := confirmedClaudeCodeHeaders()
	headers.Set("User-Agent", "claude-cli/2.1.321 (external, cli)")
	headers.Set("X-App", "cli-bg")
	payload := claudeCodeDetectionPayload(validClaudeCodeMetadataUserID)

	detection := DetectClaudeCodeRequest(headers, payload, false)
	if !detection.Confirmed || !detection.StrongSignals || !detection.NativeClient || !detection.XAppCLI {
		t.Fatalf("detection = %#v, want background session confirmed as native CLI", detection)
	}
}

func TestDetectClaudeCodeRequestRejectsUnknownXApp(t *testing.T) {
	payload := claudeCodeDetectionPayload(validClaudeCodeMetadataUserID)
	for _, value := range []string{"cli-bg2", "CLI-BG", "bg", "cli bg"} {
		t.Run(value, func(t *testing.T) {
			headers := confirmedClaudeCodeHeaders()
			headers.Set("X-App", value)
			if detection := DetectClaudeCodeRequest(headers, payload, false); detection.Confirmed || detection.XAppCLI {
				t.Fatalf("detection = %#v, want X-App %q unconfirmed", detection, value)
			}
		})
	}
}

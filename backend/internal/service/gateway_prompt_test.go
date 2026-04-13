package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsClaudeCodeClient(t *testing.T) {
	tests := []struct {
		name           string
		userAgent      string
		metadataUserID string
		want           bool
	}{
		{
			name:           "Claude Code client",
			userAgent:      "claude-cli/1.0.62 (darwin; arm64)",
			metadataUserID: "session_123e4567-e89b-12d3-a456-426614174000",
			want:           true,
		},
		{
			name:           "Claude Code without version suffix",
			userAgent:      "claude-cli/2.0.0",
			metadataUserID: "session_abc",
			want:           true,
		},
		{
			name:           "Missing metadata user_id",
			userAgent:      "claude-cli/1.0.0",
			metadataUserID: "",
			want:           false,
		},
		{
			name:           "Different user agent",
			userAgent:      "curl/7.68.0",
			metadataUserID: "user123",
			want:           false,
		},
		{
			name:           "Empty user agent",
			userAgent:      "",
			metadataUserID: "user123",
			want:           false,
		},
		{
			name:           "Similar but not Claude CLI",
			userAgent:      "claude-api/1.0.0",
			metadataUserID: "user123",
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isClaudeCodeClient(tt.userAgent, tt.metadataUserID)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSystemIncludesClaudeCodePrompt(t *testing.T) {
	tests := []struct {
		name   string
		system any
		want   bool
	}{
		{
			name:   "nil system",
			system: nil,
			want:   false,
		},
		{
			name:   "empty string",
			system: "",
			want:   false,
		},
		{
			name:   "string with Claude Code prompt",
			system: claudeCodeSystemPrompt,
			want:   true,
		},
		{
			name:   "string with different content",
			system: "You are a helpful assistant.",
			want:   false,
		},
		{
			name:   "empty array",
			system: []any{},
			want:   false,
		},
		{
			name: "array with Claude Code prompt",
			system: []any{
				map[string]any{
					"type": "text",
					"text": claudeCodeSystemPrompt,
				},
			},
			want: true,
		},
		{
			name: "array with Claude Code prompt in second position",
			system: []any{
				map[string]any{"type": "text", "text": "First prompt"},
				map[string]any{"type": "text", "text": claudeCodeSystemPrompt},
			},
			want: true,
		},
		{
			name: "array without Claude Code prompt",
			system: []any{
				map[string]any{"type": "text", "text": "Custom prompt"},
			},
			want: false,
		},
		{
			name: "array with partial match (should not match)",
			system: []any{
				map[string]any{"type": "text", "text": "You are Claude"},
			},
			want: false,
		},
		// json.RawMessage cases (conversion path: ForwardAsResponses / ForwardAsChatCompletions)
		{
			name:   "json.RawMessage string with Claude Code prompt",
			system: json.RawMessage(`"` + claudeCodeSystemPrompt + `"`),
			want:   true,
		},
		{
			name:   "json.RawMessage string without Claude Code prompt",
			system: json.RawMessage(`"You are a helpful assistant"`),
			want:   false,
		},
		{
			name:   "json.RawMessage nil (empty)",
			system: json.RawMessage(nil),
			want:   false,
		},
		{
			name:   "json.RawMessage empty string",
			system: json.RawMessage(`""`),
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := systemIncludesClaudeCodePrompt(tt.system)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestInjectClaudeCodePrompt(t *testing.T) {
	claudePrefix := strings.TrimSpace(claudeCodeSystemPrompt)

	// Every produced system array now starts with the same two blocks
	// (billing header at [0], Claude Code banner at [1]), matching real
	// claude-cli/2.1.100 capture/011 + capture/012. Tests only assert on
	// the trailing block beyond [0] and [1] (when the incoming system
	// contained additional content).
	tests := []struct {
		name          string
		body          string
		system        any
		wantSystemLen int
		// wantThirdText is the text of system[2] (the merged client content
		// block) when present, or "" when injection produces only the
		// billing header + banner pair.
		wantThirdText string
	}{
		{
			name:          "nil system",
			body:          `{"model":"claude-3"}`,
			system:        nil,
			wantSystemLen: 2,
		},
		{
			name:          "empty string system",
			body:          `{"model":"claude-3"}`,
			system:        "",
			wantSystemLen: 2,
		},
		{
			name:          "string system",
			body:          `{"model":"claude-3"}`,
			system:        "Custom prompt",
			wantSystemLen: 3,
			wantThirdText: claudePrefix + "\n\nCustom prompt",
		},
		{
			name:          "string system equals Claude Code prompt",
			body:          `{"model":"claude-3"}`,
			system:        claudeCodeSystemPrompt,
			wantSystemLen: 2,
		},
		{
			name:          "array system",
			body:          `{"model":"claude-3"}`,
			system:        []any{map[string]any{"type": "text", "text": "Custom"}},
			wantSystemLen: 3,
			wantThirdText: claudePrefix + "\n\nCustom",
		},
		{
			name: "array system with existing Claude Code prompt (should dedupe)",
			body: `{"model":"claude-3"}`,
			system: []any{
				map[string]any{"type": "text", "text": claudeCodeSystemPrompt},
				map[string]any{"type": "text", "text": "Other"},
			},
			// Existing banner is deduped; "Other" gets banner-prefixed at [2].
			wantSystemLen: 3,
			wantThirdText: claudePrefix + "\n\nOther",
		},
		{
			name:          "empty array",
			body:          `{"model":"claude-3"}`,
			system:        []any{},
			wantSystemLen: 2,
		},
		// json.RawMessage cases (conversion path: ForwardAsResponses / ForwardAsChatCompletions)
		{
			name:          "json.RawMessage string system",
			body:          `{"model":"claude-3","system":"Custom prompt"}`,
			system:        json.RawMessage(`"Custom prompt"`),
			wantSystemLen: 3,
			wantThirdText: claudePrefix + "\n\nCustom prompt",
		},
		{
			name:          "json.RawMessage nil system",
			body:          `{"model":"claude-3"}`,
			system:        json.RawMessage(nil),
			wantSystemLen: 2,
		},
		{
			name:          "json.RawMessage Claude Code prompt (should not duplicate)",
			body:          `{"model":"claude-3","system":"` + claudeCodeSystemPrompt + `"}`,
			system:        json.RawMessage(`"` + claudeCodeSystemPrompt + `"`),
			wantSystemLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := injectClaudeCodePrompt([]byte(tt.body), tt.system)

			var parsed map[string]any
			err := json.Unmarshal(result, &parsed)
			require.NoError(t, err)

			system, ok := parsed["system"].([]any)
			require.True(t, ok, "system should be an array")
			require.Len(t, system, tt.wantSystemLen)

			// system[0] is always the billing header placeholder, no cache_control.
			billing, ok := system[0].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "text", billing["type"])
			require.Equal(t, claudeCodeBillingHeaderText, billing["text"])
			_, hasCC := billing["cache_control"]
			require.False(t, hasCC, "billing header block must NOT have cache_control")

			// system[1] is always the Claude Code banner, no cache_control.
			banner, ok := system[1].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "text", banner["type"])
			require.Equal(t, claudeCodeSystemPrompt, banner["text"])
			_, hasCC = banner["cache_control"]
			require.False(t, hasCC, "banner block must NOT have cache_control")

			if tt.wantThirdText != "" {
				require.GreaterOrEqual(t, len(system), 3)
				third, ok := system[2].(map[string]any)
				require.True(t, ok)
				require.Equal(t, tt.wantThirdText, third["text"])
			}
		})
	}
}

func TestRewriteSystemForNonClaudeCode(t *testing.T) {
	ccBanner := strings.TrimSpace(claudeCodeSystemPrompt)

	tests := []struct {
		name            string
		body            string
		system          any
		wantSystemLen   int    // total system blocks count
		wantMessagesLen int    // messages array length (should stay unchanged)
		wantThirdBlock  string // system[2].text if exists (original content, CC-prefixed)
	}{
		{
			name:            "nil system - only billing + banner",
			body:            `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:          nil,
			wantSystemLen:   2,
			wantMessagesLen: 1,
		},
		{
			name:            "empty string system - only billing + banner",
			body:            `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:          "",
			wantSystemLen:   2,
			wantMessagesLen: 1,
		},
		{
			name:            "custom string system - appended as system block",
			body:            `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:          "You are a personal assistant running inside OpenClaw.",
			wantSystemLen:   3,
			wantMessagesLen: 1, // messages unchanged
			wantThirdBlock:  ccBanner + "\n\nYou are a personal assistant running inside OpenClaw.",
		},
		{
			name:            "system equals Claude Code prompt - only billing + banner",
			body:            `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:          claudeCodeSystemPrompt,
			wantSystemLen:   2,
			wantMessagesLen: 1,
		},
		{
			name: "array system with custom blocks - preserved as system blocks",
			body: `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system: []any{
				map[string]any{"type": "text", "text": "First instruction"},
				map[string]any{"type": "text", "text": "Second instruction"},
			},
			wantSystemLen:   4, // billing + banner + 2 original blocks
			wantMessagesLen: 1,
			wantThirdBlock:  ccBanner + "\n\nFirst instruction", // first block CC-prefixed
		},
		{
			name:            "empty array system - only billing + banner",
			body:            `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:          []any{},
			wantSystemLen:   2,
			wantMessagesLen: 1,
		},
		{
			name:            "json.RawMessage string system",
			body:            `{"model":"claude-3","system":"Custom prompt","messages":[{"role":"user","content":"hello"}]}`,
			system:          json.RawMessage(`"Custom prompt"`),
			wantSystemLen:   3,
			wantMessagesLen: 1,
			wantThirdBlock:  ccBanner + "\n\nCustom prompt",
		},
		{
			name:            "json.RawMessage nil system",
			body:            `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:          json.RawMessage(nil),
			wantSystemLen:   2,
			wantMessagesLen: 1,
		},
		{
			name:            "multiple original messages preserved unchanged",
			body:            `{"model":"claude-3","messages":[{"role":"user","content":"msg1"},{"role":"assistant","content":"resp1"},{"role":"user","content":"msg2"}]}`,
			system:          "Be helpful",
			wantSystemLen:   3,
			wantMessagesLen: 3, // all original messages preserved, no injection
			wantThirdBlock:  ccBanner + "\n\nBe helpful",
		},
		// Regression: leading-whitespace + CC prefix + extra instructions.
		// The raw string has leading spaces so HasPrefix(v, ccPrefix) is false,
		// causing the CC banner to be prepended. The full original text is preserved.
		{
			name:            "leading whitespace + CC prefix + extra instructions in system",
			body:            `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:          "  You are Claude Code, Anthropic's official CLI for Claude. Always respond in French.",
			wantSystemLen:   3,
			wantMessagesLen: 1,
			wantThirdBlock:  ccBanner + "\n\n  You are Claude Code, Anthropic's official CLI for Claude. Always respond in French.",
		},
		// Edge case: leading whitespace + bare banner only — no extra block needed.
		{
			name:            "leading whitespace + bare banner only - no extra block",
			body:            `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:          "  " + claudeCodeSystemPrompt + "  ",
			wantSystemLen:   2,
			wantMessagesLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rewriteSystemForNonClaudeCode([]byte(tt.body), tt.system)

			var parsed map[string]any
			err := json.Unmarshal(result, &parsed)
			require.NoError(t, err)

			// Verify system array structure
			systemArr, ok := parsed["system"].([]any)
			require.True(t, ok, "system should be an array, got %T", parsed["system"])
			require.Len(t, systemArr, tt.wantSystemLen, "system block count mismatch")

			// system[0]: billing header placeholder (no cache_control)
			billingBlock, ok := systemArr[0].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "text", billingBlock["type"])
			require.Equal(t, claudeCodeBillingHeaderText, billingBlock["text"])
			_, hasCC := billingBlock["cache_control"]
			require.False(t, hasCC, "billing header block must NOT have cache_control")

			// system[1]: Claude Code banner (no cache_control)
			bannerBlock, ok := systemArr[1].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "text", bannerBlock["type"])
			require.Equal(t, claudeCodeSystemPrompt, bannerBlock["text"])
			_, hasCC = bannerBlock["cache_control"]
			require.False(t, hasCC, "banner block must NOT have cache_control")

			// system[2]: original client content (if expected)
			if tt.wantThirdBlock != "" {
				require.GreaterOrEqual(t, len(systemArr), 3, "expected at least 3 system blocks")
				thirdBlock, ok := systemArr[2].(map[string]any)
				require.True(t, ok)
				require.Equal(t, tt.wantThirdBlock, thirdBlock["text"])
			}

			// Messages must be unchanged (no user/assistant injection)
			messages, ok := parsed["messages"].([]any)
			require.True(t, ok, "messages should be an array")
			require.Len(t, messages, tt.wantMessagesLen, "messages should not be modified")
		})
	}
}

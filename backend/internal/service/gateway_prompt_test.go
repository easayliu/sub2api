package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestIsClaudeCodeRequest_StrictContextOnly verifies that isClaudeCodeRequest
// trusts only the validator-written context flag. Spoofed UA + metadata.user_id
// must NOT be treated as CLI traffic; those requests must be routed through the
// mimic path so their body is normalised to CLI wire format before upstream.
func TestIsClaudeCodeRequest_StrictContextOnly(t *testing.T) {
	tests := []struct {
		name        string
		ctxFlag     *bool // nil: key absent, false: explicit false, true: explicit true
		userAgent   string
		metadataID  string
		wantCLIPath bool
	}{
		{
			name:        "validator confirmed CLI -> CLI path",
			ctxFlag:     boolPtr(true),
			userAgent:   "claude-cli/2.1.107 (external, cli)",
			metadataID:  `{"device_id":"abc","session_id":"sid"}`,
			wantCLIPath: true,
		},
		{
			name:        "validator rejected but UA/metadata look spoofed -> mimic (strict)",
			ctxFlag:     boolPtr(false),
			userAgent:   "claude-cli/2.1.107 (external, cli)",
			metadataID:  `{"device_id":"abc","session_id":"sid"}`,
			wantCLIPath: false,
		},
		{
			name:        "validator rejected + non-CLI UA -> mimic",
			ctxFlag:     boolPtr(false),
			userAgent:   "curl/7.68.0",
			metadataID:  "",
			wantCLIPath: false,
		},
		{
			name:        "context key absent -> mimic (safer default)",
			ctxFlag:     nil,
			userAgent:   "claude-cli/2.1.107 (external, cli)",
			metadataID:  `{"device_id":"abc"}`,
			wantCLIPath: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			if tt.ctxFlag != nil {
				ctx = SetClaudeCodeClient(ctx, *tt.ctxFlag)
			}
			parsed := &ParsedRequest{MetadataUserID: tt.metadataID}
			got := isClaudeCodeRequest(ctx, nil, parsed)
			require.Equal(t, tt.wantCLIPath, got)
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

	tests := []struct {
		name           string
		body           string
		system         any
		wantSystemLen  int
		wantFirstText  string
		wantSecondText string
	}{
		{
			name:          "nil system",
			body:          `{"model":"claude-3"}`,
			system:        nil,
			wantSystemLen: 1,
			wantFirstText: claudeCodeSystemPrompt,
		},
		{
			name:          "empty string system",
			body:          `{"model":"claude-3"}`,
			system:        "",
			wantSystemLen: 1,
			wantFirstText: claudeCodeSystemPrompt,
		},
		{
			name:           "string system",
			body:           `{"model":"claude-3"}`,
			system:         "Custom prompt",
			wantSystemLen:  2,
			wantFirstText:  claudeCodeSystemPrompt,
			wantSecondText: claudePrefix + "\n\nCustom prompt",
		},
		{
			name:          "string system equals Claude Code prompt",
			body:          `{"model":"claude-3"}`,
			system:        claudeCodeSystemPrompt,
			wantSystemLen: 1,
			wantFirstText: claudeCodeSystemPrompt,
		},
		{
			name:   "array system",
			body:   `{"model":"claude-3"}`,
			system: []any{map[string]any{"type": "text", "text": "Custom"}},
			// Claude Code + Custom = 2
			wantSystemLen:  2,
			wantFirstText:  claudeCodeSystemPrompt,
			wantSecondText: claudePrefix + "\n\nCustom",
		},
		{
			name: "array system with existing Claude Code prompt (should dedupe)",
			body: `{"model":"claude-3"}`,
			system: []any{
				map[string]any{"type": "text", "text": claudeCodeSystemPrompt},
				map[string]any{"type": "text", "text": "Other"},
			},
			// Claude Code at start + Other = 2 (deduped)
			wantSystemLen:  2,
			wantFirstText:  claudeCodeSystemPrompt,
			wantSecondText: claudePrefix + "\n\nOther",
		},
		{
			name:          "empty array",
			body:          `{"model":"claude-3"}`,
			system:        []any{},
			wantSystemLen: 1,
			wantFirstText: claudeCodeSystemPrompt,
		},
		// json.RawMessage cases (conversion path: ForwardAsResponses / ForwardAsChatCompletions)
		{
			name:           "json.RawMessage string system",
			body:           `{"model":"claude-3","system":"Custom prompt"}`,
			system:         json.RawMessage(`"Custom prompt"`),
			wantSystemLen:  2,
			wantFirstText:  claudeCodeSystemPrompt,
			wantSecondText: claudePrefix + "\n\nCustom prompt",
		},
		{
			name:          "json.RawMessage nil system",
			body:          `{"model":"claude-3"}`,
			system:        json.RawMessage(nil),
			wantSystemLen: 1,
			wantFirstText: claudeCodeSystemPrompt,
		},
		{
			name:          "json.RawMessage Claude Code prompt (should not duplicate)",
			body:          `{"model":"claude-3","system":"` + claudeCodeSystemPrompt + `"}`,
			system:        json.RawMessage(`"` + claudeCodeSystemPrompt + `"`),
			wantSystemLen: 1,
			wantFirstText: claudeCodeSystemPrompt,
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

			first, ok := system[0].(map[string]any)
			require.True(t, ok)
			require.Equal(t, tt.wantFirstText, first["text"])
			require.Equal(t, "text", first["type"])

			// Check cache_control (CLI 2.1.107 emits ttl:"1h" on every system block)
			cc, ok := first["cache_control"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "ephemeral", cc["type"])
			require.Equal(t, "1h", cc["ttl"])

			if tt.wantSecondText != "" && len(system) > 1 {
				second, ok := system[1].(map[string]any)
				require.True(t, ok)
				require.Equal(t, tt.wantSecondText, second["text"])
			}
		})
	}
}

func TestRewriteSystemForNonClaudeCode(t *testing.T) {
	const billingHeaderText = "x-anthropic-billing-header: cc_version=2.1.107.c33; cc_entrypoint=cli; cch=00000;"

	tests := []struct {
		name              string
		body              string
		system            any
		wantBlock2Text    string // system[2].text 期望值；空表示用默认 CC agent prompt
		wantBlock2Default bool   // system[2] 是否应等于 defaultClaudeCodeAgentPrompt
	}{
		{
			name:              "nil system - block2 falls back to default agent prompt",
			body:              `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:            nil,
			wantBlock2Default: true,
		},
		{
			name:              "empty string system - block2 falls back to default",
			body:              `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:            "",
			wantBlock2Default: true,
		},
		{
			name:           "custom string system - kept in system[2]",
			body:           `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:         "You are a personal assistant running inside OpenClaw.",
			wantBlock2Text: "You are a personal assistant running inside OpenClaw.",
		},
		{
			name:              "system equals Claude Code banner - block2 falls back to default",
			body:              `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:            claudeCodeSystemPrompt,
			wantBlock2Default: true,
		},
		{
			name: "array system with custom blocks - joined into system[2]",
			body: `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system: []any{
				map[string]any{"type": "text", "text": "First instruction"},
				map[string]any{"type": "text", "text": "Second instruction"},
			},
			wantBlock2Text: "First instruction\n\nSecond instruction",
		},
		{
			name:              "empty array system - block2 falls back to default",
			body:              `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:            []any{},
			wantBlock2Default: true,
		},
		{
			name:           "json.RawMessage string system",
			body:           `{"model":"claude-3","system":"Custom prompt","messages":[{"role":"user","content":"hello"}]}`,
			system:         json.RawMessage(`"Custom prompt"`),
			wantBlock2Text: "Custom prompt",
		},
		{
			name:              "json.RawMessage nil system - block2 falls back to default",
			body:              `{"model":"claude-3","messages":[{"role":"user","content":"hello"}]}`,
			system:            json.RawMessage(nil),
			wantBlock2Default: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rewriteSystemForNonClaudeCode([]byte(tt.body), tt.system)

			var parsed map[string]any
			err := json.Unmarshal(result, &parsed)
			require.NoError(t, err)

			systemArr, ok := parsed["system"].([]any)
			require.True(t, ok, "system should be an array, got %T", parsed["system"])
			require.Len(t, systemArr, 4,
				"system always has 4 blocks: billing + banner + agent + env (matches CLI 2.1.107 capture)")

			block0, ok := systemArr[0].(map[string]any)
			require.True(t, ok, "system[0] should be an object, got %T", systemArr[0])
			require.Equal(t, "text", block0["type"])
			require.Equal(t, billingHeaderText, block0["text"])
			require.Nil(t, block0["cache_control"])

			block1, ok := systemArr[1].(map[string]any)
			require.True(t, ok, "system[1] should be an object, got %T", systemArr[1])
			require.Equal(t, "text", block1["type"])
			require.Equal(t, claudeCodeSystemPrompt, block1["text"])
			require.Nil(t, block1["cache_control"])

			block2, ok := systemArr[2].(map[string]any)
			require.True(t, ok, "system[2] should be an object, got %T", systemArr[2])
			require.Equal(t, "text", block2["type"])
			if tt.wantBlock2Default {
				require.Equal(t, defaultClaudeCodeAgentPrompt, block2["text"])
			} else {
				require.Equal(t, tt.wantBlock2Text, block2["text"])
			}
			cc, ok := block2["cache_control"].(map[string]any)
			require.True(t, ok, "system[2] should have cache_control")
			require.Equal(t, "ephemeral", cc["type"])
			require.Equal(t, "1h", cc["ttl"])
			require.Equal(t, "global", cc["scope"])

			block3, ok := systemArr[3].(map[string]any)
			require.True(t, ok, "system[3] should be an object, got %T", systemArr[3])
			require.Equal(t, "text", block3["type"])
			require.Equal(t, defaultClaudeCodeEnvPrompt, block3["text"])
			require.Nil(t, block3["cache_control"],
				"system[3] (env block) must NOT carry cache_control per CLI capture")

			// 防回退：每个 block 的 JSON key 顺序必须 type→text(→cache_control)，scope 在 ttl 后
			raw := string(result)
			require.NotContains(t, raw, `{"cache_control"`, "block 不应以 cache_control 开头（字母序）")
			require.NotContains(t, raw, `{"text"`, "block 不应以 text 开头")
			require.Contains(t, raw, `"cache_control":{"type":"ephemeral","ttl":"1h","scope":"global"}`,
				"cache_control 字段顺序必须 type→ttl→scope")

			messages, ok := parsed["messages"].([]any)
			require.True(t, ok, "messages should be an array")
			var originalParsed map[string]any
			require.NoError(t, json.Unmarshal([]byte(tt.body), &originalParsed))
			originalMessages, ok := originalParsed["messages"].([]any)
			require.True(t, ok, "original messages should be an array")
			require.Len(t, messages, len(originalMessages), "messages must not be mutated")
		})
	}
}

func TestMimicCLIBodyFields(t *testing.T) {
	t.Run("non-haiku: injects thinking + context_management + output_config", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","messages":[]}`)
		out := mimicCLIBodyFields(body, "claude-opus-4-6")

		require.Equal(t, "adaptive", gjson.GetBytes(out, "thinking.type").String())
		require.Equal(t, "all", gjson.GetBytes(out, "context_management.edits.0.keep").String())
		require.Equal(t, "clear_thinking_20251015", gjson.GetBytes(out, "context_management.edits.0.type").String())
		require.Equal(t, "medium", gjson.GetBytes(out, "output_config.effort").String())
	})

	t.Run("haiku: skipped entirely", func(t *testing.T) {
		body := []byte(`{"model":"claude-haiku-4-5","messages":[]}`)
		out := mimicCLIBodyFields(body, "claude-haiku-4-5")

		require.False(t, gjson.GetBytes(out, "thinking").Exists())
		require.False(t, gjson.GetBytes(out, "context_management").Exists())
		require.False(t, gjson.GetBytes(out, "output_config").Exists())
	})

	t.Run("client-provided fields win", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","thinking":{"type":"enabled","budget_tokens":1024},"output_config":{"effort":"high"},"context_management":{"custom":true},"messages":[]}`)
		out := mimicCLIBodyFields(body, "claude-opus-4-6")

		require.Equal(t, "enabled", gjson.GetBytes(out, "thinking.type").String())
		require.Equal(t, int64(1024), gjson.GetBytes(out, "thinking.budget_tokens").Int())
		require.Equal(t, "high", gjson.GetBytes(out, "output_config.effort").String())
		require.True(t, gjson.GetBytes(out, "context_management.custom").Bool())
	})
}

func TestMimicCLIMessages(t *testing.T) {
	t.Run("string content wrapped to array + cache_control attached", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
		out := mimicCLIMessages(body)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal(out, &parsed))

		msgs, ok := parsed["messages"].([]any)
		require.True(t, ok, "messages should be an array")
		require.Len(t, msgs, 1)
		firstMsg, ok := msgs[0].(map[string]any)
		require.True(t, ok, "first message should be a map")
		content, ok := firstMsg["content"].([]any)
		require.True(t, ok, "content should be an array")
		require.Len(t, content, 1)
		block := content[0].(map[string]any)
		require.Equal(t, "text", block["type"])
		require.Equal(t, "hello", block["text"])
		cc := block["cache_control"].(map[string]any)
		require.Equal(t, "ephemeral", cc["type"])
		require.Equal(t, "1h", cc["ttl"])

		// 防回退：JSON key 必须按 CLI wire format 顺序输出，不能字母序
		raw := string(out)
		typePos := strings.Index(raw, `"type":"text"`)
		textPos := strings.Index(raw, `"text":"hello"`)
		ccPos := strings.Index(raw, `"cache_control"`)
		require.True(t, typePos < textPos && textPos < ccPos,
			"content block keys must be type→text→cache_control, got: %s", raw)
		// cache_control 内部 type 必须先于 ttl
		ccTypePos := strings.Index(raw, `"cache_control":{"type"`)
		require.NotEqual(t, -1, ccTypePos, "cache_control must start with type, got: %s", raw)
	})

	t.Run("array content - cache_control on last text block", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"first"},{"type":"text","text":"last"}]}]}`)
		out := mimicCLIMessages(body)

		content := gjson.GetBytes(out, "messages.0.content").Array()
		require.Len(t, content, 2)
		require.False(t, content[0].Get("cache_control").Exists())
		require.Equal(t, "ephemeral", content[1].Get("cache_control.type").String())
		require.Equal(t, "1h", content[1].Get("cache_control.ttl").String())
	})

	t.Run("multi-turn - cache_control only on LAST user's last text block", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":"q1"},{"role":"assistant","content":"a1"},{"role":"user","content":"q2"}]}`)
		out := mimicCLIMessages(body)

		require.False(t, gjson.GetBytes(out, "messages.0.content.0.cache_control").Exists())
		require.False(t, gjson.GetBytes(out, "messages.1.content.0.cache_control").Exists())
		require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.2.content.0.cache_control.type").String())
	})

	t.Run("idempotent: existing cache_control left alone", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}`)
		out := mimicCLIMessages(body)
		require.Equal(t, "5m", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
	})

	t.Run("tool_result-only user message - skip cache_control", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}]}`)
		out := mimicCLIMessages(body)
		require.False(t, gjson.GetBytes(out, "messages.0.content.0.cache_control").Exists())
	})

	t.Run("no messages - no-op", func(t *testing.T) {
		body := []byte(`{"model":"claude-3"}`)
		out := mimicCLIMessages(body)
		require.Equal(t, string(body), string(out))
	})
}

package service

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
)

func assertJSONTokenOrder(t *testing.T, body string, tokens ...string) {
	t.Helper()

	last := -1
	for _, token := range tokens {
		pos := strings.Index(body, token)
		require.NotEqualf(t, -1, pos, "missing token %s in body %s", token, body)
		require.Greaterf(t, pos, last, "token %s should appear after previous tokens in body %s", token, body)
		last = pos
	}
}

func TestReplaceModelInBody_PreservesTopLevelFieldOrder(t *testing.T) {
	svc := &GatewayService{}
	body := []byte(`{"alpha":1,"model":"claude-3-5-sonnet-latest","messages":[],"omega":2}`)

	result := svc.replaceModelInBody(body, "claude-3-5-sonnet-20241022")
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"model"`, `"messages"`, `"omega"`)
	require.Contains(t, resultStr, `"model":"claude-3-5-sonnet-20241022"`)
}

func TestNormalizeClaudeOAuthRequestBody_PreservesTopLevelFieldOrder(t *testing.T) {
	body := []byte(`{"alpha":1,"model":"claude-3-5-sonnet-latest","temperature":0.2,"system":"You are OpenCode, the best coding agent on the planet.","messages":[],"tool_choice":{"type":"auto"},"omega":2}`)

	result, modelID := normalizeClaudeOAuthRequestBody(body, "claude-3-5-sonnet-latest", claudeOAuthNormalizeOptions{
		injectMetadata: true,
		metadataUserID: "user-1",
	})
	resultStr := string(result)

	require.Equal(t, claude.NormalizeModelID("claude-3-5-sonnet-latest"), modelID)
	// temperature 和 tool_choice 现在透传（不再被删除），保持原字段位置不变。
	assertJSONTokenOrder(t, resultStr,
		`"alpha"`, `"model"`, `"temperature"`, `"system"`, `"messages"`, `"tool_choice"`, `"omega"`, `"tools"`, `"metadata"`)
	require.Contains(t, resultStr, `"temperature":0.2`)
	require.Contains(t, resultStr, `"tool_choice":{"type":"auto"}`)
	require.Contains(t, resultStr, `"system":"`+claudeCodeSystemPrompt+`"`)
	require.Contains(t, resultStr, `"tools":[]`)
	require.Contains(t, resultStr, `"metadata":{"user_id":"user-1"}`)
}

// TestNormalizeClaudeOAuthRequestBody_PreservesTemperatureAndToolChoice
// pins the regression: real claude-cli/2.1.100 sends `temperature` (capture/011)
// and Anthropic API documents `tool_choice` as a supported field. The historical
// "delete temperature/tool_choice" workaround silently dropped client config and
// has been removed.
func TestNormalizeClaudeOAuthRequestBody_PreservesTemperatureAndToolChoice(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-5","temperature":0.7,"tool_choice":{"type":"any"},"messages":[]}`)

	result, _ := normalizeClaudeOAuthRequestBody(body, "claude-opus-4-5", claudeOAuthNormalizeOptions{})
	resultStr := string(result)

	require.Contains(t, resultStr, `"temperature":0.7`)
	require.Contains(t, resultStr, `"tool_choice":{"type":"any"}`)
}

func TestInjectClaudeCodePrompt_PreservesFieldOrder(t *testing.T) {
	body := []byte(`{"alpha":1,"system":[{"id":"block-1","type":"text","text":"Custom"}],"messages":[],"omega":2}`)

	result := injectClaudeCodePrompt(body, []any{
		map[string]any{"id": "block-1", "type": "text", "text": "Custom"},
	})
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"system"`, `"messages"`, `"omega"`)
	require.Contains(t, resultStr, `{"id":"block-1","type":"text","text":"`+claudeCodeSystemPrompt+`\n\nCustom"}`)
}

func TestEnforceCacheControlLimit_PreservesTopLevelFieldOrder(t *testing.T) {
	body := []byte(`{"alpha":1,"system":[{"type":"text","text":"s1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"s2","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"m1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"m2","cache_control":{"type":"ephemeral"}},{"type":"text","text":"m3","cache_control":{"type":"ephemeral"}}]}],"omega":2}`)

	result := enforceCacheControlLimit(body)
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"system"`, `"messages"`, `"omega"`)
	require.Equal(t, 4, strings.Count(resultStr, `"cache_control"`))
}

// TestEnforceCacheControlLimit_GlobalScopePreserved verifies that the shape
// emitted by claude-cli/2.1.100 with prompt-caching-scope-2026-01-05 beta
// (one scope:global block in system + one per-conversation block in messages)
// round-trips through the limit enforcer without mangling scope/ttl fields
// or dropping any cache_control entry.
func TestEnforceCacheControlLimit_GlobalScopePreserved(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"banner"},{"type":"text","text":"long agent prompt","cache_control":{"scope":"global","ttl":"1h","type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"ttl":"1h","type":"ephemeral"}}]}]}`)

	result := enforceCacheControlLimit(body)
	resultStr := string(result)

	// Both cache_control entries are retained — no deletions.
	require.Equal(t, 2, strings.Count(resultStr, `"cache_control"`))
	// scope / ttl fields preserved verbatim on the global block.
	require.Contains(t, resultStr, `"cache_control":{"scope":"global","ttl":"1h","type":"ephemeral"}`)
	// Per-conversation block's ttl is preserved.
	require.Contains(t, resultStr, `"cache_control":{"ttl":"1h","type":"ephemeral"}`)
}

// TestEnforceCacheControlLimit_GlobalScopeExcludedFromCount verifies that
// global-scope blocks are exempt from the per-request 4-block limit: with
// 1 global + 4 per-conversation blocks (5 total, 4 countable), no deletion
// should occur.
func TestEnforceCacheControlLimit_GlobalScopeExcludedFromCount(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"agent","cache_control":{"scope":"global","ttl":"1h","type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"u1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"u2","cache_control":{"type":"ephemeral"}},{"type":"text","text":"u3","cache_control":{"type":"ephemeral"}},{"type":"text","text":"u4","cache_control":{"type":"ephemeral"}}]}]}`)

	result := enforceCacheControlLimit(body)
	resultStr := string(result)

	// All 5 cache_control entries retained (1 global + 4 per-conversation).
	require.Equal(t, 5, strings.Count(resultStr, `"cache_control"`))
	require.Contains(t, resultStr, `"scope":"global"`)
}

// TestEnforceCacheControlLimit_GlobalScopeWithOverflowCountsOnlyLocal
// verifies that when per-conversation blocks exceed the 4-limit, only
// per-conversation entries are trimmed while global-scope blocks are
// always preserved.
func TestEnforceCacheControlLimit_GlobalScopeWithOverflowCountsOnlyLocal(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"agent","cache_control":{"scope":"global","ttl":"1h","type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"u1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"u2","cache_control":{"type":"ephemeral"}},{"type":"text","text":"u3","cache_control":{"type":"ephemeral"}},{"type":"text","text":"u4","cache_control":{"type":"ephemeral"}},{"type":"text","text":"u5","cache_control":{"type":"ephemeral"}}]}]}`)

	result := enforceCacheControlLimit(body)
	resultStr := string(result)

	// 1 global (exempt) + 5 local (1 over limit) -> 1 local removed, 4 remain.
	// Total cache_control entries in output: 1 global + 4 local = 5.
	require.Equal(t, 5, strings.Count(resultStr, `"cache_control"`))
	// Global scope block survives.
	require.Contains(t, resultStr, `"scope":"global"`)
}

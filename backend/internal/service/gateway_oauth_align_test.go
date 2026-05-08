package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAlignUserContextSystemReminder verifies that the per-request context
// system-reminder block in messages is rewritten to match CLI 2.1.133
// OAuth-mode shape: # userEmail injection from Account.Extra["email_address"]
// and YYYY/MM/DD -> YYYY-MM-DD date normalization.
func TestAlignUserContextSystemReminder(t *testing.T) {
	const (
		apikeyShape = "<system-reminder>\nAs you answer the user's questions, you can use the following context:\n# currentDate\nToday's date is 2026/05/08.\n\n      IMPORTANT: this context may or may not be relevant to your tasks. You should not respond to this context unless it is highly relevant to your task.\n</system-reminder>\n"
		oauthShape  = "<system-reminder>\nAs you answer the user's questions, you can use the following context:\n# userEmail\nThe user's email address is cork-said-dutiful@duck.com.\n# currentDate\nToday's date is 2026-05-08.\n\n      IMPORTANT: this context may or may not be relevant to your tasks. You should not respond to this context unless it is highly relevant to your task.\n</system-reminder>\n"
	)

	bodyWith := func(reminderText string) []byte {
		// Build a minimal body with one user message carrying the reminder.
		// Use a literal string so we control the exact wire bytes.
		return []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":` + jsonEncode(reminderText) + `}]}]}`)
	}

	t.Run("injects email and normalizes date when both are missing", func(t *testing.T) {
		account := &Account{Extra: map[string]any{"email_address": "cork-said-dutiful@duck.com"}}
		got := alignUserContextSystemReminder(bodyWith(apikeyShape), account)
		gotText := firstReminderText(t, got)
		require.Equal(t, oauthShape, gotText, "transformed block must equal sub-mode shape")
	})

	t.Run("idempotent on already-OAuth-shape input", func(t *testing.T) {
		account := &Account{Extra: map[string]any{"email_address": "cork-said-dutiful@duck.com"}}
		got := alignUserContextSystemReminder(bodyWith(oauthShape), account)
		require.Equal(t, oauthShape, firstReminderText(t, got))
	})

	t.Run("date-only fix when account has no email", func(t *testing.T) {
		account := &Account{Extra: map[string]any{}}
		got := alignUserContextSystemReminder(bodyWith(apikeyShape), account)
		gotText := firstReminderText(t, got)
		// Date got fixed.
		require.Contains(t, gotText, "Today's date is 2026-05-08.")
		require.NotContains(t, gotText, "2026/05/08")
		// userEmail must NOT be fabricated when we don't know it.
		require.NotContains(t, gotText, "# userEmail")
	})

	t.Run("noop when block prefix doesn't match", func(t *testing.T) {
		account := &Account{Extra: map[string]any{"email_address": "x@y.z"}}
		other := "<system-reminder>\nUnrelated reminder block.\n</system-reminder>"
		body := bodyWith(other)
		got := alignUserContextSystemReminder(body, account)
		require.Equal(t, other, firstReminderText(t, got))
	})

	t.Run("noop when account is nil", func(t *testing.T) {
		body := bodyWith(apikeyShape)
		got := alignUserContextSystemReminder(body, nil)
		require.Equal(t, apikeyShape, firstReminderText(t, got))
	})

	t.Run("preserves email already present, still fixes date", func(t *testing.T) {
		// Edge case: a partially-shaped block that already has userEmail but
		// still uses slash dates. We must not double-inject email but should
		// still normalize the date.
		mixed := strings.Replace(oauthShape, "2026-05-08", "2026/05/08", 1)
		account := &Account{Extra: map[string]any{"email_address": "cork-said-dutiful@duck.com"}}
		got := alignUserContextSystemReminder(bodyWith(mixed), account)
		require.Equal(t, oauthShape, firstReminderText(t, got))
	})
}

// TestSplitMergedSystemForOAuth verifies the API-key-mode 3-block system[]
// gets restructured into the CLI 2.1.133 OAuth-mode 4-block shape.
func TestSplitMergedSystemForOAuth(t *testing.T) {
	// Build a sys[2] that mirrors the boundary captured in capture/0508:
	// exactly 9925 chars of agent-instructions text followed by
	// "\n\n# Text output (does not apply to tool calls)\n...".
	agentText := strings.Repeat("a", 9925)
	require.Len(t, agentText, 9925)
	tail := "\n\n# Text output (does not apply to tool calls)\nAssume users can't see most tool calls"
	mergedSys2 := agentText + tail

	body := []byte(`{
		"system": [
			{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.133.045; cc_entrypoint=cli; cch=00000;"},
			{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude.","cache_control":{"type":"ephemeral"}},
			{"type":"text","text":` + jsonEncode(mergedSys2) + `,"cache_control":{"type":"ephemeral"}}
		]
	}`)

	got := splitMergedSystemForOAuth(body)

	// Verify 4 blocks now.
	system := jsonArray(t, got, "system")
	require.Len(t, system, 4, "system must be split into 4 blocks")

	// sys[0]: billing header preserved.
	require.Equal(t, "x-anthropic-billing-header: cc_version=2.1.133.045; cc_entrypoint=cli; cch=00000;", jsonString(t, system[0], "text"))
	require.Nil(t, jsonGet(t, system[0], "cache_control"), "sys[0] must have no cache_control")

	// sys[1]: identity, cache_control removed.
	require.Equal(t, claudeCodeSystemPrompt, jsonString(t, system[1], "text"))
	require.Nil(t, jsonGet(t, system[1], "cache_control"), "sys[1] cache_control must be removed")

	// sys[2]: agent prompt with global+1h cache.
	require.Equal(t, agentText, jsonString(t, system[2], "text"))
	require.Len(t, jsonString(t, system[2], "text"), 9925)
	cc2 := jsonObj(t, system[2], "cache_control")
	require.Equal(t, "ephemeral", cc2["type"])
	require.Equal(t, "1h", cc2["ttl"])
	require.Equal(t, "global", cc2["scope"])

	// sys[3]: trailing content with 1h cache, no scope:global.
	require.Equal(t, "# Text output (does not apply to tool calls)\nAssume users can't see most tool calls", jsonString(t, system[3], "text"))
	cc3 := jsonObj(t, system[3], "cache_control")
	require.Equal(t, "ephemeral", cc3["type"])
	require.Equal(t, "1h", cc3["ttl"])
	_, hasScope := cc3["scope"]
	require.False(t, hasScope, "sys[3] must not carry scope:global")
}

func TestSplitMergedSystemForOAuthNoOpCases(t *testing.T) {
	// Already 4-block (real OAuth or mimic-built): leave alone.
	t.Run("4-block already", func(t *testing.T) {
		body := []byte(`{"system":[{"text":"a"},{"text":"b"},{"text":"c"},{"text":"d"}]}`)
		got := splitMergedSystemForOAuth(body)
		require.JSONEq(t, string(body), string(got))
	})

	// Wrong identity text: don't risk a wrong split.
	t.Run("identity text mismatch", func(t *testing.T) {
		body := []byte(`{"system":[{"text":"billing"},{"text":"custom prompt"},{"text":"merged"}]}`)
		got := splitMergedSystemForOAuth(body)
		require.JSONEq(t, string(body), string(got))
	})

	// Boundary marker at wrong offset (custom system content).
	t.Run("boundary at wrong offset", func(t *testing.T) {
		body := []byte(`{"system":[{"text":"billing"},{"text":"You are Claude Code, Anthropic's official CLI for Claude."},{"text":"short body\n\n# Text output (does not apply to tool calls)\nx"}]}`)
		got := splitMergedSystemForOAuth(body)
		require.JSONEq(t, string(body), string(got))
	})
}

// --- test helpers (only used by this file) ---

func jsonEncode(s string) string {
	// Escape minimally for embedding into a JSON string literal in tests.
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + r.Replace(s) + `"`
}

func firstReminderText(t *testing.T, body []byte) string {
	t.Helper()
	// Walk messages[0].content[0].text — tests construct bodies in that shape.
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(body, &parsed))
	msgs, _ := parsed["messages"].([]any)
	require.NotEmpty(t, msgs)
	first, _ := msgs[0].(map[string]any)
	content, _ := first["content"].([]any)
	require.NotEmpty(t, content)
	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)
	return text
}

func jsonArray(t *testing.T, body []byte, key string) []any {
	t.Helper()
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(body, &parsed))
	arr, ok := parsed[key].([]any)
	require.True(t, ok, "expected %s to be array", key)
	return arr
}

func jsonGet(t *testing.T, block any, key string) any {
	t.Helper()
	m, ok := block.(map[string]any)
	require.True(t, ok)
	return m[key]
}

func jsonString(t *testing.T, block any, key string) string {
	t.Helper()
	v := jsonGet(t, block, key)
	s, ok := v.(string)
	require.True(t, ok, "expected %s to be string, got %T", key, v)
	return s
}

func jsonObj(t *testing.T, block any, key string) map[string]any {
	t.Helper()
	v := jsonGet(t, block, key)
	m, ok := v.(map[string]any)
	require.True(t, ok, "expected %s to be object", key)
	return m
}

package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpgradeCLICacheTTL(t *testing.T) {
	tests := []struct {
		name string
		body string
		// assertFn inspects the upgraded body. Return empty list for no checks.
		assertSystem   func(t *testing.T, system []any)
		assertMessages func(t *testing.T, messages []any)
	}{
		{
			name: "agent-instructions block gets ttl + scope:global",
			body: `{
				"system": [
					{"type":"text","text":"x-anthropic-billing-header: cch=abc;"},
					{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude.","cache_control":{"type":"ephemeral"}},
					{"type":"text","text":"\nYou are an interactive agent that helps users with software engineering tasks. Use the instructions...","cache_control":{"type":"ephemeral"}}
				]
			}`,
			assertSystem: func(t *testing.T, system []any) {
				require.Len(t, system, 3)

				// Block 0: billing header, no cc -> unchanged.
				b0 := system[0].(map[string]any)
				_, hasCC := b0["cache_control"]
				require.False(t, hasCC)

				// Block 1: Claude Code banner -> ttl:"1h", no scope.
				b1cc := system[1].(map[string]any)["cache_control"].(map[string]any)
				require.Equal(t, "ephemeral", b1cc["type"])
				require.Equal(t, "1h", b1cc["ttl"])
				_, hasScope := b1cc["scope"]
				require.False(t, hasScope)

				// Block 2: agent instructions -> ttl:"1h" AND scope:"global".
				b2cc := system[2].(map[string]any)["cache_control"].(map[string]any)
				require.Equal(t, "ephemeral", b2cc["type"])
				require.Equal(t, "1h", b2cc["ttl"])
				require.Equal(t, "global", b2cc["scope"])
			},
		},
		{
			name: "agent-instructions match survives merged Text output trailer",
			body: `{
				"system": [
					{"type":"text","text":"\nYou are an interactive agent that helps users with software engineering tasks.\n...\n# Text output\n...","cache_control":{"type":"ephemeral"}}
				]
			}`,
			assertSystem: func(t *testing.T, system []any) {
				cc := system[0].(map[string]any)["cache_control"].(map[string]any)
				require.Equal(t, "global", cc["scope"])
				require.Equal(t, "1h", cc["ttl"])
			},
		},
		{
			name: "existing ttl is left untouched",
			body: `{
				"system": [
					{"type":"text","text":"\nYou are an interactive agent that helps users with software engineering tasks.","cache_control":{"type":"ephemeral","ttl":"5m"}}
				]
			}`,
			assertSystem: func(t *testing.T, system []any) {
				cc := system[0].(map[string]any)["cache_control"].(map[string]any)
				require.Equal(t, "5m", cc["ttl"])
				_, hasScope := cc["scope"]
				require.False(t, hasScope)
			},
		},
		{
			name: "user message ephemeral gets ttl",
			body: `{
				"messages": [
					{"role":"user","content":[
						{"type":"text","text":"hi"},
						{"type":"text","text":"nihao","cache_control":{"type":"ephemeral"}}
					]}
				]
			}`,
			assertMessages: func(t *testing.T, messages []any) {
				content := messages[0].(map[string]any)["content"].([]any)
				cc := content[1].(map[string]any)["cache_control"].(map[string]any)
				require.Equal(t, "1h", cc["ttl"])
			},
		},
		{
			name: "block without cache_control untouched; no messages section ok",
			body: `{
				"system": [
					{"type":"text","text":"plain block, no cc"}
				]
			}`,
			assertSystem: func(t *testing.T, system []any) {
				_, hasCC := system[0].(map[string]any)["cache_control"]
				require.False(t, hasCC)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := upgradeCLICacheTTL([]byte(tt.body))

			var parsed map[string]any
			require.NoError(t, json.Unmarshal(got, &parsed))

			if tt.assertSystem != nil {
				system, ok := parsed["system"].([]any)
				require.True(t, ok, "expected system array")
				tt.assertSystem(t, system)
			}
			if tt.assertMessages != nil {
				messages, ok := parsed["messages"].([]any)
				require.True(t, ok, "expected messages array")
				tt.assertMessages(t, messages)
			}
		})
	}
}

func TestUpgradeCLICacheTTLIdempotent(t *testing.T) {
	body := []byte(`{
		"system": [
			{"type":"text","text":"\nYou are an interactive agent that helps users with software engineering tasks.","cache_control":{"type":"ephemeral"}}
		],
		"messages": [
			{"role":"user","content":[{"type":"text","text":"nihao","cache_control":{"type":"ephemeral"}}]}
		]
	}`)

	first := upgradeCLICacheTTL(body)
	second := upgradeCLICacheTTL(first)
	require.JSONEq(t, string(first), string(second))
}

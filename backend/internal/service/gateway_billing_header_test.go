package service

import (
	"fmt"
	"testing"

	"github.com/cespare/xxhash/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSyncBillingHeaderVersion(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		userAgent string
		wantSub   string // substring expected in result
		unchanged bool   // expect body to remain the same
	}{
		{
			name:      "replaces cc_version preserving message-derived suffix",
			body:      `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.81.df2; cc_entrypoint=cli; cch=00000;"},{"type":"text","text":"You are Claude Code.","cache_control":{"type":"ephemeral"}}],"messages":[]}`,
			userAgent: "claude-cli/2.1.22 (external, cli)",
			wantSub:   "cc_version=2.1.22.df2",
		},
		{
			name:      "no billing header in system",
			body:      `{"system":[{"type":"text","text":"You are Claude Code."}],"messages":[]}`,
			userAgent: "claude-cli/2.1.22",
			unchanged: true,
		},
		{
			name:      "no system field",
			body:      `{"messages":[]}`,
			userAgent: "claude-cli/2.1.22",
			unchanged: true,
		},
		{
			name:      "user-agent without version",
			body:      `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.81; cc_entrypoint=cli; cch=00000;"}],"messages":[]}`,
			userAgent: "Mozilla/5.0",
			unchanged: true,
		},
		{
			name:      "empty user-agent",
			body:      `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.81; cc_entrypoint=cli; cch=00000;"}],"messages":[]}`,
			userAgent: "",
			unchanged: true,
		},
		{
			name:      "version already matches",
			body:      `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.22; cc_entrypoint=cli; cch=00000;"}],"messages":[]}`,
			userAgent: "claude-cli/2.1.22",
			unchanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := syncBillingHeaderVersion([]byte(tt.body), tt.userAgent)
			if tt.unchanged {
				assert.Equal(t, tt.body, string(result), "body should remain unchanged")
			} else {
				assert.Contains(t, string(result), tt.wantSub)
				// Ensure old semver is gone
				assert.NotContains(t, string(result), "cc_version=2.1.81")
			}
		})
	}
}

func TestSignBillingHeaderCCH(t *testing.T) {
	t.Run("replaces placeholder with hash", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.63.a43; cc_entrypoint=cli; cch=00000;"}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
		result := signBillingHeaderCCH(body)

		// Should not have the placeholder anymore
		assert.NotContains(t, string(result), "cch=00000")

		// Should have a 5 hex-char cch value
		billingText := gjson.GetBytes(result, "system.0.text").String()
		require.Contains(t, billingText, "cch=")
		assert.Regexp(t, `cch=[0-9a-f]{5};`, billingText)
	})

	t.Run("no placeholder - body unchanged", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.63; cc_entrypoint=cli; cch=abcde;"}],"messages":[]}`)
		result := signBillingHeaderCCH(body)
		assert.Equal(t, string(body), string(result))
	})

	t.Run("no billing header - body unchanged", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"You are Claude Code."}],"messages":[]}`)
		result := signBillingHeaderCCH(body)
		assert.Equal(t, string(body), string(result))
	})

	t.Run("cch=00000 in user content is not touched", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.63; cc_entrypoint=cli; cch=00000;"}],"messages":[{"role":"user","content":[{"type":"text","text":"keep literal cch=00000 in this message"}]}]}`)
		result := signBillingHeaderCCH(body)

		// Billing header should be signed
		billingText := gjson.GetBytes(result, "system.0.text").String()
		assert.NotContains(t, billingText, "cch=00000")

		// User message should keep its literal cch=00000
		userText := gjson.GetBytes(result, "messages.0.content.0.text").String()
		assert.Contains(t, userText, "cch=00000")
	})

	t.Run("signing is deterministic", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.63; cc_entrypoint=cli; cch=00000;"}],"messages":[{"role":"user","content":"hi"}]}`)
		r1 := signBillingHeaderCCH(body)
		body2 := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.63; cc_entrypoint=cli; cch=00000;"}],"messages":[{"role":"user","content":"hi"}]}`)
		r2 := signBillingHeaderCCH(body2)
		assert.Equal(t, string(r1), string(r2))
	})

	t.Run("matches reference algorithm", func(t *testing.T) {
		// Verify: signBillingHeaderCCH(body) produces cch = xxHash64(body_with_placeholder, seed) & 0xFFFFF
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.63.a43; cc_entrypoint=cli; cch=00000;"}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
		expectedCCH := fmt.Sprintf("%05x", xxHash64Seeded(body, cchSeed)&0xFFFFF)

		result := signBillingHeaderCCH(body)
		billingText := gjson.GetBytes(result, "system.0.text").String()
		assert.Contains(t, billingText, "cch="+expectedCCH+";")
	})
}

func TestResignBillingHeaderCCH(t *testing.T) {
	t.Run("re-signs pre-computed CCH after body modification", func(t *testing.T) {
		// Simulate: client computed cch on original body, then proxy modified body
		original := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.104.e50; cc_entrypoint=cli; cch=00000;"}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
		signed := signBillingHeaderCCH(original)
		billingText := gjson.GetBytes(signed, "system.0.text").String()
		assert.NotContains(t, billingText, "cch=00000")

		// Now re-sign — should produce the same value (body unchanged)
		resigned := resignBillingHeaderCCH(signed)
		resignedBilling := gjson.GetBytes(resigned, "system.0.text").String()
		assert.Equal(t, billingText, resignedBilling)
	})

	t.Run("produces valid 5 hex-char cch", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.104.e50; cc_entrypoint=cli; cch=a0ff5;"}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
		result := resignBillingHeaderCCH(body)
		billingText := gjson.GetBytes(result, "system.0.text").String()
		assert.Regexp(t, `cch=[0-9a-f]{5};`, billingText)
		assert.NotContains(t, billingText, "cch=a0ff5")
	})

	t.Run("idempotent - resign twice equals resign once", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.104.e50; cc_entrypoint=cli; cch=abcde;"}],"messages":[{"role":"user","content":"hi"}]}`)
		r1 := resignBillingHeaderCCH(body)
		r2 := resignBillingHeaderCCH(r1)
		assert.Equal(t, string(r1), string(r2))
	})

	t.Run("no billing header - body unchanged", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"You are Claude Code."}],"messages":[]}`)
		result := resignBillingHeaderCCH(body)
		assert.Equal(t, string(body), string(result))
	})

	t.Run("cch=00000 placeholder - signs normally", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.104.e50; cc_entrypoint=cli; cch=00000;"}],"messages":[{"role":"user","content":"hi"}]}`)
		result := resignBillingHeaderCCH(body)
		billingText := gjson.GetBytes(result, "system.0.text").String()
		assert.NotContains(t, billingText, "cch=00000")
		assert.Regexp(t, `cch=[0-9a-f]{5};`, billingText)
	})

	t.Run("matches reference: reset to 00000 then sign", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.104.e50; cc_entrypoint=cli; cch=12345;"}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)

		// Manual: reset cch to 00000, then compute expected hash
		resetBody := cchAnyValueRe.ReplaceAll(body, []byte("${1}00000${3}"))
		expectedCCH := fmt.Sprintf("%05x", xxHash64Seeded(resetBody, cchSeed)&0xFFFFF)

		result := resignBillingHeaderCCH(body)
		billingText := gjson.GetBytes(result, "system.0.text").String()
		assert.Contains(t, billingText, "cch="+expectedCCH+";")
	})
}

func TestXXHash64Seeded(t *testing.T) {
	t.Run("matches cespare/xxhash for seed 0", func(t *testing.T) {
		inputs := []string{"", "a", "hello world", "The quick brown fox jumps over the lazy dog"}
		for _, s := range inputs {
			data := []byte(s)
			expected := xxhash.Sum64(data)
			got := xxHash64Seeded(data, 0)
			assert.Equal(t, expected, got, "mismatch for input %q", s)
		}
	})

	t.Run("large input matches cespare", func(t *testing.T) {
		data := make([]byte, 256)
		for i := range data {
			data[i] = byte(i)
		}
		expected := xxhash.Sum64(data)
		got := xxHash64Seeded(data, 0)
		assert.Equal(t, expected, got)
	})

	t.Run("deterministic with custom seed", func(t *testing.T) {
		data := []byte("hello world")
		h1 := xxHash64Seeded(data, cchSeed)
		h2 := xxHash64Seeded(data, cchSeed)
		assert.Equal(t, h1, h2)
	})

	t.Run("different seeds produce different results", func(t *testing.T) {
		data := []byte("test data for hashing")
		h1 := xxHash64Seeded(data, 0)
		h2 := xxHash64Seeded(data, cchSeed)
		assert.NotEqual(t, h1, h2)
	})
}

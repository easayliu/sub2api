package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/cespare/xxhash/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// sha256Prefix3 is a reference implementation of the suffix hash used by
// tests to independently verify computeBillingHeaderSuffix output.
func sha256Prefix3(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])[:3]
}

func TestComputeBillingHeaderSuffix(t *testing.T) {
	t.Run("reference example from CLI v2.1.77 spec", func(t *testing.T) {
		// Documented algorithm:
		//   first user text: "Hello, how are you?"
		//   chars at [4,7,20]: 'o', 'h', '0' (pos 20 missing -> default)
		//   sha256("59cf53e54c78" + "oh0" + "2.1.77")[:3] = "b88"
		body := []byte(`{"messages":[{"role":"user","content":"Hello, how are you?"}]}`)
		assert.Equal(t, "b88", computeBillingHeaderSuffix(body, "2.1.77"))
	})

	t.Run("content as array (single block) samples that block", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"Hello, how are you?"}]}]}`)
		assert.Equal(t, "b88", computeBillingHeaderSuffix(body, "2.1.77"))
	})

	t.Run("system-reminder prefix blocks are skipped", func(t *testing.T) {
		// CLI prepends <system-reminder> blocks to every user turn; the real
		// user input is the first non-skipped block.
		body := []byte(`{"messages":[{"role":"user","content":[
			{"type":"text","text":"<system-reminder>\nirrelevant prefix\n</system-reminder>"},
			{"type":"text","text":"<system-reminder>\nanother prefix block here\n</system-reminder>"},
			{"type":"text","text":"Hello, how are you?"}
		]}]}`)
		assert.Equal(t, "b88", computeBillingHeaderSuffix(body, "2.1.77"))
	})

	t.Run("matches real CLI capture 2.1.114 / 你好 -> 069", func(t *testing.T) {
		// Verified against capture 004_204859 (first user message = 4
		// system-reminder blocks + "你好"). Expected cc_version=2.1.114.069.
		body := []byte(`{"messages":[{"role":"user","content":[
			{"type":"text","text":"<system-reminder>\ntools\n</system-reminder>"},
			{"type":"text","text":"<system-reminder>\nmcp\n</system-reminder>"},
			{"type":"text","text":"<system-reminder>\nskills\n</system-reminder>"},
			{"type":"text","text":"<system-reminder>\ncontext\n</system-reminder>"},
			{"type":"text","text":"你好"}
		]}]}`)
		assert.Equal(t, "069", computeBillingHeaderSuffix(body, "2.1.114"))
	})

	t.Run("ignores later user turns - uses only first user message", func(t *testing.T) {
		// Verified against capture 005_210245: even in a multi-turn session,
		// the suffix is derived from messages[0] only, not the latest user
		// turn. Both capture 004 (1 turn) and 005 (3 turns, same first turn)
		// produced cc_version=2.1.114.069.
		body := []byte(`{"messages":[
			{"role":"user","content":[{"type":"text","text":"你好"}]},
			{"role":"assistant","content":[{"type":"text","text":"hi"}]},
			{"role":"user","content":[{"type":"text","text":"你能做什么呢"}]}
		]}`)
		assert.Equal(t, "069", computeBillingHeaderSuffix(body, "2.1.114"))
	})

	t.Run("skips non-user messages", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"assistant","content":"ignored"},{"role":"user","content":"Hello, how are you?"}]}`)
		assert.Equal(t, "b88", computeBillingHeaderSuffix(body, "2.1.77"))
	})

	t.Run("empty messages defaults all chars to '0'", func(t *testing.T) {
		body := []byte(`{"messages":[]}`)
		expected := sha256Prefix3(billingHeaderSuffixSalt + "000" + "2.1.110")
		assert.Equal(t, expected, computeBillingHeaderSuffix(body, "2.1.110"))
	})

	t.Run("missing messages field defaults all chars to '0'", func(t *testing.T) {
		body := []byte(`{}`)
		expected := sha256Prefix3(billingHeaderSuffixSalt + "000" + "2.1.110")
		assert.Equal(t, expected, computeBillingHeaderSuffix(body, "2.1.110"))
	})

	t.Run("short text pads missing positions with '0'", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
		// runes: 'h','i' (len 2). Positions 4,7,20 all out of range -> "000".
		expected := sha256Prefix3(billingHeaderSuffixSalt + "000" + "2.1.110")
		assert.Equal(t, expected, computeBillingHeaderSuffix(body, "2.1.110"))
	})

	t.Run("user content with only non-text blocks yields empty text", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"y"}]}]}`)
		expected := sha256Prefix3(billingHeaderSuffixSalt + "000" + "2.1.110")
		assert.Equal(t, expected, computeBillingHeaderSuffix(body, "2.1.110"))
	})

	t.Run("suffix changes with version", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":"Hello, how are you?"}]}`)
		s110 := computeBillingHeaderSuffix(body, "2.1.110")
		s113 := computeBillingHeaderSuffix(body, "2.1.113")
		assert.NotEqual(t, s110, s113)
	})

	t.Run("suffix changes when sampled positions differ", func(t *testing.T) {
		// Only positions 4, 7, 20 are sampled. Vary those to see a difference.
		body1 := []byte(`{"messages":[{"role":"user","content":"abcd-ef-hijklmnopqrs-uvw"}]}`)
		body2 := []byte(`{"messages":[{"role":"user","content":"abcdXefXhijklmnopqrsXuvw"}]}`)
		s1 := computeBillingHeaderSuffix(body1, "2.1.110")
		s2 := computeBillingHeaderSuffix(body2, "2.1.110")
		assert.NotEqual(t, s1, s2)
	})

	t.Run("compact next turn: samples compact summary block, not user input", func(t *testing.T) {
		// capture/0521/014 (CLI 2.1.146, post-/compact next turn):
		//   [0..2] <system-reminder>...   skipped
		//   [3]    "This session is being continued from a previous..."  <-- sampled
		//   [4]    <local-command-caveat>...                              skipped
		//   [5]    <command-name>/compact</command-name>...               kept but [3] wins
		//   [6]    <local-command-stdout>...                              skipped
		//   [7]    "nihaowe"                                              user input, ignored
		// Block [3] rune[4]/[7]/[20] = ' ', 's', 'g' -> chars=" sg" -> 75f.
		body := []byte(`{"messages":[{"role":"user","content":[
			{"type":"text","text":"<system-reminder>\nirrelevant\n</system-reminder>"},
			{"type":"text","text":"<system-reminder>\nmore stuff\n</system-reminder>"},
			{"type":"text","text":"<system-reminder>\nskills etc\n</system-reminder>"},
			{"type":"text","text":"This session is being continued from a previous conversation that ran out of context"},
			{"type":"text","text":"<local-command-caveat>Caveat: ...</local-command-caveat>"},
			{"type":"text","text":"<command-name>/compact</command-name>"},
			{"type":"text","text":"<local-command-stdout>Compacted</local-command-stdout>"},
			{"type":"text","text":"nihaowe"}
		]}]}`)
		assert.Equal(t, "75f", computeBillingHeaderSuffix(body, "2.1.146"))
	})

	t.Run("clear next turn: samples <command-name>/clear block", func(t *testing.T) {
		// capture/0521/025 and 036 (CLI 2.1.146, post-/clear next turn):
		//   [0..3] <system-reminder>...   skipped
		//   [4]    <local-command-caveat> skipped (<local- prefix)
		//   [5]    "<command-name>/clear</command-name>..."  <-- sampled
		//   [6]    <local-command-stdout> skipped
		//   [7]    user input             ignored
		// 036 used "你睡", 025 used "nihao"; both yield 793 because the sample
		// is the /clear command-name block, not the user's new text.
		// Block [5] rune[4]/[7]/[20] = 'm', 'd', '<' -> chars="md<" -> 793.
		body := []byte(`{"messages":[{"role":"user","content":[
			{"type":"text","text":"<system-reminder>\ntools\n</system-reminder>"},
			{"type":"text","text":"<system-reminder>\nmcp\n</system-reminder>"},
			{"type":"text","text":"<system-reminder>\nskills\n</system-reminder>"},
			{"type":"text","text":"<system-reminder>\ncontext\n</system-reminder>"},
			{"type":"text","text":"<local-command-caveat>Caveat: ...</local-command-caveat>"},
			{"type":"text","text":"<command-name>/clear</command-name>\n            <command-message>clear</command-message>\n            <command-args></command-args>"},
			{"type":"text","text":"<local-command-stdout></local-command-stdout>"},
			{"type":"text","text":"你睡"}
		]}]}`)
		assert.Equal(t, "793", computeBillingHeaderSuffix(body, "2.1.146"))
	})

	t.Run("compact summary single block: samples it directly", func(t *testing.T) {
		// capture/0521/007 and 039 (CLI 2.1.146, the request that delivers the
		// compact summary itself): messages[0].content is a single text block
		// starting with "<session>\nThis session is being continued...".
		// Suffix is 037 for 2.1.146; chars at [4]/[7]/[20] = 's', 'n', 'o'.
		body := []byte(`{"messages":[{"role":"user","content":[
			{"type":"text","text":"<session>\nThis session is being continued from a previous conversation"}
		]}]}`)
		assert.Equal(t, "037", computeBillingHeaderSuffix(body, "2.1.146"))
	})
}

func TestPickBillingHeaderSampleText(t *testing.T) {
	t.Run("empty list returns empty string", func(t *testing.T) {
		assert.Equal(t, "", pickBillingHeaderSampleText(nil))
		assert.Equal(t, "", pickBillingHeaderSampleText([]string{}))
	})

	t.Run("single non-skipped block is returned", func(t *testing.T) {
		assert.Equal(t, "hello", pickBillingHeaderSampleText([]string{"hello"}))
	})

	t.Run("skips system-reminder prefix", func(t *testing.T) {
		got := pickBillingHeaderSampleText([]string{
			"<system-reminder>foo</system-reminder>",
			"real input",
		})
		assert.Equal(t, "real input", got)
	})

	t.Run("skips <local- prefix (caveat and stdout)", func(t *testing.T) {
		got := pickBillingHeaderSampleText([]string{
			"<system-reminder>x</system-reminder>",
			"<local-command-caveat>...</local-command-caveat>",
			"<command-name>/clear</command-name>",
			"<local-command-stdout></local-command-stdout>",
			"user input",
		})
		assert.Equal(t, "<command-name>/clear</command-name>", got)
	})

	t.Run("does not skip <command-name> blocks", func(t *testing.T) {
		// <command-name> represents the slash command the user typed; CLI
		// keys the suffix off this block when running /clear (see captures
		// 025/026/036/038). Only <system-reminder> and <local-* prefixes
		// are dropped.
		got := pickBillingHeaderSampleText([]string{
			"<command-name>/clear</command-name>",
			"<local-command-stdout></local-command-stdout>",
		})
		assert.Equal(t, "<command-name>/clear</command-name>", got)
	})

	t.Run("falls back to last entry when every block is skipped", func(t *testing.T) {
		got := pickBillingHeaderSampleText([]string{
			"<system-reminder>a</system-reminder>",
			"<local-command-stdout></local-command-stdout>",
		})
		assert.Equal(t, "<local-command-stdout></local-command-stdout>", got)
	})
}

func TestSyncBillingHeaderVersion(t *testing.T) {
	t.Run("no billing header in system - unchanged", func(t *testing.T) {
		body := `{"system":[{"type":"text","text":"You are Claude Code."}],"messages":[]}`
		result := syncBillingHeaderVersion([]byte(body), "claude-cli/2.1.22")
		assert.Equal(t, body, string(result))
	})

	t.Run("no system field - unchanged", func(t *testing.T) {
		body := `{"messages":[]}`
		result := syncBillingHeaderVersion([]byte(body), "claude-cli/2.1.22")
		assert.Equal(t, body, string(result))
	})

	t.Run("user-agent without version - unchanged", func(t *testing.T) {
		body := `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.81; cc_entrypoint=cli; cch=00000;"}],"messages":[]}`
		result := syncBillingHeaderVersion([]byte(body), "Mozilla/5.0")
		assert.Equal(t, body, string(result))
	})

	t.Run("empty user-agent - unchanged", func(t *testing.T) {
		body := `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.81; cc_entrypoint=cli; cch=00000;"}],"messages":[]}`
		result := syncBillingHeaderVersion([]byte(body), "")
		assert.Equal(t, body, string(result))
	})

	t.Run("rewrites version and recomputes suffix dynamically", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.104.abc; cc_entrypoint=cli; cch=00000;"}],"messages":[{"role":"user","content":"Hello, how are you?"}]}`)
		result := syncBillingHeaderVersion(body, "claude-cli/2.1.110 (external, cli)")
		expectedSuffix := computeBillingHeaderSuffix(body, "2.1.110")
		assert.Contains(t, string(result), "cc_version=2.1.110."+expectedSuffix)
		assert.NotContains(t, string(result), "cc_version=2.1.104")
	})

	t.Run("matches reference spec for 2.1.77 / Hello example", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.81.df2; cc_entrypoint=cli; cch=00000;"}],"messages":[{"role":"user","content":"Hello, how are you?"}]}`)
		result := syncBillingHeaderVersion(body, "claude-cli/2.1.77")
		assert.Contains(t, string(result), "cc_version=2.1.77.b88")
	})

	t.Run("adds suffix when body omits one", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.22; cc_entrypoint=cli; cch=00000;"}],"messages":[]}`)
		result := syncBillingHeaderVersion(body, "claude-cli/2.1.22")
		expectedSuffix := computeBillingHeaderSuffix(body, "2.1.22")
		assert.Contains(t, string(result), "cc_version=2.1.22."+expectedSuffix)
	})

	t.Run("rewrites 2.1.113 with dynamic suffix", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.110.610; cc_entrypoint=cli; cch=00000;"}],"messages":[{"role":"user","content":"Hello, how are you?"}]}`)
		result := syncBillingHeaderVersion(body, "claude-cli/2.1.113 (external, cli)")
		expectedSuffix := computeBillingHeaderSuffix(body, "2.1.113")
		assert.Contains(t, string(result), "cc_version=2.1.113."+expectedSuffix)
	})
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

func TestResetBillingHeaderCCH(t *testing.T) {
	t.Run("resets real signed cch back to placeholder", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.107.c33; cc_entrypoint=cli; cch=a1b2c;"}],"messages":[]}`)
		result := resetBillingHeaderCCH(body)
		billingText := gjson.GetBytes(result, "system.0.text").String()
		assert.Contains(t, billingText, "cch=00000;")
		assert.NotContains(t, billingText, "cch=a1b2c")
	})

	t.Run("placeholder already - body unchanged", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.107; cc_entrypoint=cli; cch=00000;"}],"messages":[]}`)
		result := resetBillingHeaderCCH(body)
		assert.Equal(t, string(body), string(result))
	})

	t.Run("no billing header - body unchanged", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"You are Claude Code."}],"messages":[]}`)
		result := resetBillingHeaderCCH(body)
		assert.Equal(t, string(body), string(result))
	})

	t.Run("literal cch in user content is not touched", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.107; cc_entrypoint=cli; cch=deadb;"}],"messages":[{"role":"user","content":[{"type":"text","text":"keep literal cch=cafe1 here"}]}]}`)
		result := resetBillingHeaderCCH(body)
		billingText := gjson.GetBytes(result, "system.0.text").String()
		assert.Contains(t, billingText, "cch=00000;")
		userText := gjson.GetBytes(result, "messages.0.content.0.text").String()
		assert.Contains(t, userText, "cch=cafe1")
	})

	t.Run("sign then reset round-trip yields placeholder", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.63.a43; cc_entrypoint=cli; cch=00000;"}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
		signed := signBillingHeaderCCH(body)
		require.NotContains(t, string(signed), "cch=00000")
		reset := resetBillingHeaderCCH(signed)
		assert.Contains(t, string(reset), "cch=00000;")
	})
}

func TestNormalizeBillingHeaderEntrypoint(t *testing.T) {
	t.Run("cli stays cli - no-op", func(t *testing.T) {
		body := `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.123.d8c; cc_entrypoint=cli; cch=00000;"}],"messages":[]}`
		result := normalizeBillingHeaderEntrypoint([]byte(body))
		assert.Equal(t, body, string(result))
	})

	t.Run("rewrites sdk entrypoint to cli", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.123.d8c; cc_entrypoint=claude_code_sdk_python; cch=00000;"}],"messages":[]}`)
		result := normalizeBillingHeaderEntrypoint(body)
		billingText := gjson.GetBytes(result, "system.0.text").String()
		assert.Contains(t, billingText, "cc_entrypoint=cli;")
		assert.NotContains(t, billingText, "claude_code_sdk_python")
	})

	t.Run("rewrites hyphenated value to cli", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.123.d8c; cc_entrypoint=vscode-ext; cch=00000;"}],"messages":[]}`)
		result := normalizeBillingHeaderEntrypoint(body)
		billingText := gjson.GetBytes(result, "system.0.text").String()
		assert.Contains(t, billingText, "cc_entrypoint=cli;")
		assert.NotContains(t, billingText, "vscode-ext")
	})

	t.Run("preserves cc_version and cch around rewrite", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.110.610; cc_entrypoint=external; cch=a1b2c;"}],"messages":[]}`)
		result := normalizeBillingHeaderEntrypoint(body)
		billingText := gjson.GetBytes(result, "system.0.text").String()
		assert.Contains(t, billingText, "cc_version=2.1.110.610")
		assert.Contains(t, billingText, "cc_entrypoint=cli;")
		assert.Contains(t, billingText, "cch=a1b2c;")
	})

	t.Run("no billing header - body unchanged", func(t *testing.T) {
		body := `{"system":[{"type":"text","text":"You are Claude Code."}],"messages":[]}`
		result := normalizeBillingHeaderEntrypoint([]byte(body))
		assert.Equal(t, body, string(result))
	})

	t.Run("no system field - body unchanged", func(t *testing.T) {
		body := `{"messages":[]}`
		result := normalizeBillingHeaderEntrypoint([]byte(body))
		assert.Equal(t, body, string(result))
	})

	t.Run("system as string - body unchanged", func(t *testing.T) {
		body := `{"system":"You are Claude.","messages":[]}`
		result := normalizeBillingHeaderEntrypoint([]byte(body))
		assert.Equal(t, body, string(result))
	})

	t.Run("cc_entrypoint in user content is not touched", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.123.d8c; cc_entrypoint=external; cch=00000;"}],"messages":[{"role":"user","content":[{"type":"text","text":"keep literal cc_entrypoint=other; in this message"}]}]}`)
		result := normalizeBillingHeaderEntrypoint(body)
		billingText := gjson.GetBytes(result, "system.0.text").String()
		assert.Contains(t, billingText, "cc_entrypoint=cli;")
		userText := gjson.GetBytes(result, "messages.0.content.0.text").String()
		assert.Contains(t, userText, "cc_entrypoint=other;")
	})

	t.Run("missing cc_entrypoint - body unchanged", func(t *testing.T) {
		body := `{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.123.d8c; cch=00000;"}],"messages":[]}`
		result := normalizeBillingHeaderEntrypoint([]byte(body))
		assert.Equal(t, body, string(result))
	})

	t.Run("idempotent across two calls", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.123.d8c; cc_entrypoint=claude_code_sdk_typescript; cch=00000;"}],"messages":[]}`)
		first := normalizeBillingHeaderEntrypoint(body)
		second := normalizeBillingHeaderEntrypoint(first)
		assert.Equal(t, string(first), string(second))
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

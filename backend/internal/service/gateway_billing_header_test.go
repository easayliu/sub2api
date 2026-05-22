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

	t.Run("/mcp next turn: skips <command-name>, samples trailing user URL", func(t *testing.T) {
		// 2026-05-22 18:22 production reject (CLI 2.1.138, post-/mcp turn).
		// Block list approximated; CLI samples the trailing user input
		// (URL string), not the <command-name>/mcp wrapper.
		// Trailing block "https://cert.rctech.ac.t..." at rune[4]/[7]/[20] =
		// 's', '/', 'a' -> chars="s/a" -> dc7 for ver 2.1.138.
		body := []byte(`{"messages":[{"role":"user","content":[
			{"type":"text","text":"<system-reminder>\n# MCP\n</system-reminder>"},
			{"type":"text","text":"<system-reminder>\nThe following\n</system-reminder>"},
			{"type":"text","text":"<system-reminder>\nAs you\n</system-reminder>"},
			{"type":"text","text":"<command-name>/mcp</command-name>\n  <command-message>mcp</command-message>"},
			{"type":"text","text":"<local-command-stdout>MCP server status...</local-command-stdout>"},
			{"type":"text","text":"https://cert.rctech.ac.th/foo"}
		]}]}`)
		assert.Equal(t, "dc7", computeBillingHeaderSuffix(body, "2.1.138"))
	})

	t.Run("/clear next turn: also skips <command-name>, samples trailing user input", func(t *testing.T) {
		// 旧 capture 025 / 036 显示 parsed_suffix=793 来自 <command-name>/clear，
		// 但新生产 (2026-05-22 18:22) 证实 CLI 已统一跳过 <command-name>；这里
		// 验证我们的新规则——直接取末尾用户文本。
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
		// "你睡" 2 runes only -> chars at [4,7,20] all out of range -> "000".
		// sha256(salt + "000" + "2.1.146")[:3] independently verified below.
		expected := sha256Prefix3(billingHeaderSuffixSalt + "000" + "2.1.146")
		assert.Equal(t, expected, computeBillingHeaderSuffix(body, "2.1.146"))
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

func TestStripInlinedSystemReminders(t *testing.T) {
	t.Run("no system-reminder close tag - returned unchanged", func(t *testing.T) {
		assert.Equal(t, "hello world", stripInlinedSystemReminders("hello world"))
	})

	t.Run("empty string - returned unchanged", func(t *testing.T) {
		assert.Equal(t, "", stripInlinedSystemReminders(""))
	})

	t.Run("single SR wrapper + trailing text - returns trailing", func(t *testing.T) {
		in := "<system-reminder>foo</system-reminder>\nuser typed text"
		assert.Equal(t, "user typed text", stripInlinedSystemReminders(in))
	})

	t.Run("multiple SR wrappers + trailing compact summary", func(t *testing.T) {
		in := "<system-reminder>tools</system-reminder>\n" +
			"<system-reminder>mcp</system-reminder>\n" +
			"<system-reminder>skills</system-reminder>\n" +
			"This session is being continued from a previous conversation"
		assert.Equal(t,
			"This session is being continued from a previous conversation",
			stripInlinedSystemReminders(in))
	})

	t.Run("trailing only whitespace after last close tag - fall back to inner of last SR pair", func(t *testing.T) {
		// 现行规则：trailing 为空时回退到最后一个 <sr>...</sr> 的 inner
		in := "<system-reminder>foo</system-reminder>\n   \n"
		assert.Equal(t, "foo", stripInlinedSystemReminders(in))
	})

	t.Run("close tag at very end - returns inner of last SR pair", func(t *testing.T) {
		// 单 SR 全裹整段：返回 inner
		in := "<system-reminder>real user content</system-reminder>"
		assert.Equal(t, "real user content", stripInlinedSystemReminders(in))
	})

	t.Run("multiple SR concatenated with close at end - returns last SR inner", func(t *testing.T) {
		// 多 SR 拼接，最后一个 </sr> 在末尾：取最后一对 SR 内部
		in := "<system-reminder>tools</system-reminder>\n" +
			"<system-reminder>mcp</system-reminder>\n" +
			"<system-reminder>This session is being continued from a previous conversation</system-reminder>"
		assert.Equal(t,
			"This session is being continued from a previous conversation",
			stripInlinedSystemReminders(in))
	})

	t.Run("close tag at end but inner is empty - fall back to original", func(t *testing.T) {
		// 退化情况：<sr></sr> 内部为空，fallback 到原文
		in := "<system-reminder></system-reminder>"
		assert.Equal(t, in, stripInlinedSystemReminders(in))
	})

	t.Run("close at end but no matching open tag - fall back to original", func(t *testing.T) {
		// 仅有 close 标签，没有 open（理论上不该发生）
		in := "some content</system-reminder>"
		assert.Equal(t, in, stripInlinedSystemReminders(in))
	})

	t.Run("close tag in user text but no wrapper - treated as boundary anyway", func(t *testing.T) {
		// Edge case: if a user literally types "</system-reminder>" the rule
		// will sample whatever follows. Acceptable since this is exotic input.
		in := "user says </system-reminder> then more"
		assert.Equal(t, "then more", stripInlinedSystemReminders(in))
	})

	t.Run("matches historical forge corpus suffixes - trailing after last close tag", func(t *testing.T) {
		// Validates the analytical finding that the production forge corpus
		// (string-form messages[0].content starting with <system-reminder>) is
		// actually a flattened array-form turn whose trailing block is the
		// compact-summary "This session is being co...". The parsed_suffix
		// recorded in the reject logs algebraically matches the suffix derived
		// from that trailing segment for the corresponding CLI version.
		flatten := func(trailing string) string {
			return "<system-reminder>tools</system-reminder>\n" +
				"<system-reminder>mcp</system-reminder>\n" +
				"<system-reminder>skills</system-reminder>\n" +
				trailing
		}
		// Trailing must begin with "This session is being co" so positions
		// [4]/[7]/[20] -> ' ', 's', 'g' (chars=" sg") match the array-form
		// captures we have for post-/compact next turns.
		trailing := "This session is being continued from a previous conversation"

		cases := []struct {
			ver, wantSuffix, reason string
		}{
			{"2.1.138", "4ba", "17:52 production reject parsed_suffix"},
			{"2.1.143", "7a0", "10:55 / 14:37 production reject parsed_suffix"},
			{"2.1.144", "585", "18:09 production reject parsed_suffix"},
		}
		for _, c := range cases {
			body := []byte(`{"messages":[{"role":"user","content":"` +
				flatten(trailing) + `"}]}`)
			got := computeBillingHeaderSuffix(body, c.ver)
			assert.Equal(t, c.wantSuffix, got, c.reason)
		}
	})

	t.Run("matches historical forge corpus - close tag at very end (last SR inner)", func(t *testing.T) {
		// 15:49 production reject (ver 2.1.145, parsed 20b): the entire string
		// ends with </system-reminder>, indicating multiple SR blocks where the
		// last <sr>...</sr> contains the compact-summary text. Verify the new
		// "fall back to last SR inner" path produces 20b for ver=2.1.145 when
		// the last SR inner starts with "This session is being co...".
		inner := "This session is being continued from a previous conversation"
		flattened := "<system-reminder>tools</system-reminder>\n" +
			"<system-reminder>mcp</system-reminder>\n" +
			"<system-reminder>" + inner + "</system-reminder>"
		body := []byte(`{"messages":[{"role":"user","content":"` + flattened + `"}]}`)
		assert.Equal(t, "20b", computeBillingHeaderSuffix(body, "2.1.145"))
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

	t.Run("skips <local- (caveat/stdout) and <command-name>, takes user input", func(t *testing.T) {
		// 2026-05-22 production observation (/mcp turn): CLI samples the
		// user's input that follows the slash command, not the
		// <command-name> wrapper itself.
		got := pickBillingHeaderSampleText([]string{
			"<system-reminder>x</system-reminder>",
			"<local-command-caveat>...</local-command-caveat>",
			"<command-name>/clear</command-name>",
			"<local-command-stdout></local-command-stdout>",
			"user input",
		})
		assert.Equal(t, "user input", got)
	})

	t.Run("skips <command-name> blocks too", func(t *testing.T) {
		// <command-name> is now in the skip list (alongside <system-reminder>
		// and <local-*). When followed only by other skipped blocks, the
		// picker falls back to the last entry — but in real CLI traffic
		// there is always a trailing user-authored block.
		got := pickBillingHeaderSampleText([]string{
			"<command-name>/clear</command-name>",
			"<local-command-stdout></local-command-stdout>",
		})
		// All skipped → fallback returns the last entry.
		assert.Equal(t, "<local-command-stdout></local-command-stdout>", got)
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

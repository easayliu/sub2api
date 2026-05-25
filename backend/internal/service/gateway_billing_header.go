package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ccVersionWithSuffixRe matches cc_version=X.Y.Z with an optional trailing
// build-hash suffix (e.g. ".610"). Used when rewriting to the target value.
var ccVersionWithSuffixRe = regexp.MustCompile(`cc_version=\d+\.\d+\.\d+(?:\.[0-9a-f]+)?`)

// billingHeaderSuffixSalt is the fixed 12-hex-char salt used by the CLI's
// cc_version suffix derivation algorithm (introduced in v2.1.77).
const billingHeaderSuffixSalt = "59cf53e54c78"

// billingHeaderSuffixPositions are the 0-indexed character positions in the
// first user message text used as input to the cc_version suffix hash.
var billingHeaderSuffixPositions = [...]int{4, 7, 20}

// billingHeaderSampleSkipPrefixes lists text-block prefixes the official CLI
// always skips when picking the "first user message text" that feeds the
// suffix hash. They mark blocks the CLI injects itself (environment
// scaffolding + local /-command products); the user-authored payload is
// whichever block comes after them.
//
//   - <system-reminder> — environment / tools / instructions wrappers
//   - <local-          — local-command-caveat / local-command-stdout
//
// Note <command-name> is intentionally NOT in this unconditional skip
// list; its handling depends on the surrounding stdout block — see
// pickBillingHeaderSampleText for the conditional rule.
var billingHeaderSampleSkipPrefixes = []string{
	"<system-reminder>",
	"<local-",
}

const (
	commandNameOpenTag        = "<command-name>"
	localCommandCaveatOpenTag = "<local-command-caveat>"
)

// pickBillingHeaderSampleText selects, from text-block contents in
// messages[0].content order, the block the official CLI keys its
// cc_version suffix hash off of.
//
// Rule:
//   - Unconditionally skip <system-reminder> and <local-* (caveat/stdout)
//   - For <command-name> blocks, presence of a <local-command-caveat>
//     anywhere in the block list decides:
//     · caveat PRESENT → the slash command is the "user intent" for this
//     turn (history of local commands exists, the new turn IS the
//     command itself), so SAMPLE <command-name>.
//     · caveat ABSENT  → the slash command is a one-off transition and
//     the user's typed follow-up is the real "first user text", so
//     SKIP <command-name> and continue looking.
//   - Fall back to the last entry if every block is skipped (defensive;
//     real CLI traffic always leaves at least one payload block).
//
// The caveat-presence rule was reverse-engineered from:
//   - capture/0521/025/036 + 2026-05-22 18:48 /clear: caveat present →
//     CLI sampled <command-name>/clear (chars "md<")
//   - 2026-05-22 18:22 /mcp first run: caveat absent → CLI sampled trailing
//     user URL (chars "s/a"), not <command-name>/mcp
//   - 2026-05-23 21:51 /mcp invoked twice in session: caveat present →
//     CLI sampled <command-name>/mcp (chars "mdc") despite the trailing
//     user prompt being a real Chinese question
//
// /compact is unaffected because its compact-summary block sits before
// <command-name> and is picked first regardless of caveat state.
func pickBillingHeaderSampleText(texts []string) string {
	hasCaveat := hasLocalCommandCaveat(texts)
	for _, t := range texts {
		if billingHeaderSampleTextShouldSkip(t) {
			continue
		}
		if strings.HasPrefix(t, commandNameOpenTag) && !hasCaveat {
			continue
		}
		return t
	}
	if n := len(texts); n > 0 {
		return texts[n-1]
	}
	return ""
}

func billingHeaderSampleTextShouldSkip(text string) bool {
	for _, p := range billingHeaderSampleSkipPrefixes {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

// hasLocalCommandCaveat reports whether any block starts with
// <local-command-caveat>. CLI injects that block when the conversation
// already contains historical local-command records — see
// pickBillingHeaderSampleText for how its presence flips the
// <command-name> sampling decision.
func hasLocalCommandCaveat(texts []string) bool {
	for _, t := range texts {
		if strings.HasPrefix(t, localCommandCaveatOpenTag) {
			return true
		}
	}
	return false
}

// inlinedSystemReminderOpenTag / inlinedSystemReminderCloseTag bracket a
// <system-reminder>...</...> wrapper that some middlewares inline into a
// single string when they flatten an array-form first user message. Used by
// stripInlinedSystemReminders to recover the user-authored payload for
// suffix derivation.
const (
	inlinedSystemReminderOpenTag  = "<system-reminder>"
	inlinedSystemReminderCloseTag = "</system-reminder>"
)

// inlinedSystemReminderPairRe matches a single <system-reminder>...</system-reminder>
// pair (non-greedy) in flattened string-form content. Used by
// extractInlinedSystemReminderInners for the reverse-suffix-match fallback.
var inlinedSystemReminderPairRe = regexp.MustCompile(
	`(?s)<system-reminder>(.*?)</system-reminder>`,
)

// minReverseSuffixInnerRunes is the minimum rune length an SR inner must
// have to be a candidate in the reverse-suffix-match fallback. Anything
// shorter than 21 runes has positions [4]/[7]/[20] partly out of range and
// would default '0' chars — too easy to trivially match by chance.
const minReverseSuffixInnerRunes = 21

// extractInlinedSystemReminderInners returns the inner contents of every
// <system-reminder>...</system-reminder> pair embedded inside a flattened
// string-form messages[0].content. Leading whitespace is trimmed from each
// inner so SHA256 sampling lines up with the unflattened array-form block.
// Returns only inners of length >= minReverseSuffixInnerRunes; shorter
// inners are dropped to avoid weak '0'-padding collisions in the
// reverse-suffix-match fallback.
func extractInlinedSystemReminderInners(text string) []string {
	matches := inlinedSystemReminderPairRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		inner := strings.TrimLeft(m[1], " \t\r\n")
		if utf8RuneCount(inner) < minReverseSuffixInnerRunes {
			continue
		}
		out = append(out, inner)
	}
	return out
}

// utf8RuneCount returns the rune count of s. Wrapped so callers in this
// package don't all need to import unicode/utf8 just for one symbol.
func utf8RuneCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

// reverseMatchInlinedSRInner reports whether any <system-reminder> inner in
// the flattened string-form content contains a starting offset whose
// chars at [+4, +7, +20] hash to parsedSuffix under the v2.1.77+
// derivation. Used as a fallback when our primary picker disagrees with
// parsedSuffix — covers middleware/forge layouts where:
//
//   - multi-SR string with compact summary in a non-last SR (offset 0 hit
//     on one of the inners)
//   - single-SR wrap containing the compact summary as a substring inside
//     a larger tool-call/result blob (sliding window hits at the offset
//     where "This session is being co..." begins)
//
// The strict minimum-inner-length guard (see minReverseSuffixInnerRunes)
// prevents trivial collisions on whitespace-only / very short inners.
// Within each qualifying inner, every rune offset i where
// runes[i+4]/[i+7]/[i+20] are all in range is tried.
func reverseMatchInlinedSRInner(text, version, parsedSuffix string) bool {
	for _, inner := range extractInlinedSystemReminderInners(text) {
		if reverseSuffixMatchAnyOffset(inner, version, parsedSuffix) {
			return true
		}
	}
	return false
}

// compactSummaryAnchorText is the deterministic prefix of the
// post-/compact "first user text" block emitted by Claude Code CLI.
// Finding its offset in a string-form inner is conclusive proof that the
// flattened body contains a real compact-summary segment (or someone has
// deliberately constructed one inside their fake body).
const compactSummaryAnchorText = "This session is being continued"

// findCompactSummaryAnchorOffsets returns every rune-offset within text
// where compactSummaryAnchorText begins. Empty result means the body has
// no compact-summary marker at all — strong signal that any parsed_suffix
// matching the " sg" derivation is replayed/forged rather than computed
// from this body.
//
// Iterates byte-level via strings.Index then converts byte offset to rune
// offset, so multi-byte content (CJK / emoji before the anchor) is
// reported in the rune-position the suffix algorithm actually samples on.
func findCompactSummaryAnchorOffsets(text string) []int {
	if text == "" {
		return nil
	}
	var out []int
	start := 0
	for start < len(text) {
		idx := strings.Index(text[start:], compactSummaryAnchorText)
		if idx < 0 {
			break
		}
		byteIdx := start + idx
		runeIdx := utf8RuneCount(text[:byteIdx])
		out = append(out, runeIdx)
		start = byteIdx + len(compactSummaryAnchorText)
	}
	return out
}

// reverseSuffixMatchAnyOffset scans every rune offset i in text where
// positions [i+4]/[i+7]/[i+20] are all in range, computing the
// v2.1.77+ suffix from those chars and returning true on first match.
// Requires text length >= minReverseSuffixInnerRunes so that at least
// offset 0 is a viable candidate. SHA256 per offset is microsecond-cheap;
// even a 27k-rune inner finishes in tens of milliseconds and only runs on
// the fallback path (post primary mismatch).
func reverseSuffixMatchAnyOffset(text, version, parsedSuffix string) bool {
	runes := []rune(text)
	n := len(runes)
	if n < minReverseSuffixInnerRunes {
		return false
	}
	for i := 0; i+20 < n; i++ {
		chars := []rune{runes[i+4], runes[i+7], runes[i+20]}
		sum := sha256.Sum256([]byte(billingHeaderSuffixSalt + string(chars) + version))
		if hex.EncodeToString(sum[:])[:3] == parsedSuffix {
			return true
		}
	}
	return false
}

// stringContentOfFirstUserMessage returns messages[0].content if and only
// if it is a string (the flattened form). Returns ("", false) for array-
// form or any other shape — the reverse-suffix-match fallback only applies
// to string-form bodies.
func stringContentOfFirstUserMessage(body map[string]any) (string, bool) {
	if body == nil {
		return "", false
	}
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return "", false
	}
	m0, ok := msgs[0].(map[string]any)
	if !ok {
		return "", false
	}
	s, ok := m0["content"].(string)
	return s, ok
}

// stripInlinedSystemReminders unwraps string-form messages[0].content whose
// payload is an array-form turn that some middleware has concatenated into a
// single string. Two flattening shapes are handled:
//
//  1. SR wrappers followed by user text:
//     "<sr>...</sr><sr>...</sr>...real user text"
//     → take text after the last </system-reminder>.
//  2. SR wrappers with the close tag at the very end of the string (either a
//     single SR wraps the whole payload, or multiple SR blocks are joined
//     and the last block contains the user-authored content):
//     "<sr>...</sr><sr>real user text</sr>"
//     → fall back to the inner content of the last <sr>...</sr> pair.
//
// Returns the original text unchanged when neither shape applies (no close
// tag at all, or the recovered inner is empty), so the suffix derivation at
// least has stable input.
//
// Verified against the production reject corpus (ver 2.1.138 / 2.1.143 /
// 2.1.144 / 2.1.145) where parsed_suffix algebraically matches the suffix
// derived from the recovered trailing or last-SR-inner segment of an
// equivalent array-form body.
func stripInlinedSystemReminders(text string) string {
	closeIdx := strings.LastIndex(text, inlinedSystemReminderCloseTag)
	if closeIdx < 0 {
		return text
	}
	trailing := strings.TrimLeft(text[closeIdx+len(inlinedSystemReminderCloseTag):], " \t\r\n")
	if trailing != "" {
		return trailing
	}
	// Close tag at the very end with no trailing user text — peel back into
	// the matching last <system-reminder> open tag and return its inner.
	openIdx := strings.LastIndex(text[:closeIdx], inlinedSystemReminderOpenTag)
	if openIdx < 0 {
		return text
	}
	inner := strings.TrimLeft(text[openIdx+len(inlinedSystemReminderOpenTag):closeIdx], " \t\r\n")
	if inner == "" {
		return text
	}
	return inner
}

// ccEntrypointRe matches cc_entrypoint=<value> inside an x-anthropic-billing-header
// text block. Scoped to the header prefix to avoid touching user content.
var ccEntrypointRe = regexp.MustCompile(`(x-anthropic-billing-header:[^"]*?\bcc_entrypoint=)([A-Za-z0-9_-]+)(\s*;)`)

// cchPlaceholderRe matches the cch=00000 placeholder in billing header text,
// scoped to x-anthropic-billing-header to avoid touching user content.
var cchPlaceholderRe = regexp.MustCompile(`(x-anthropic-billing-header:[^"]*?\bcch=)(00000)(;)`)

// cchAnyValueRe matches any 5-hex-char cch value in billing header text,
// scoped to x-anthropic-billing-header to avoid touching user content.
var cchAnyValueRe = regexp.MustCompile(`(x-anthropic-billing-header:[^"]*?\bcch=)([0-9a-f]{5})(;)`)

const cchSeed uint64 = 0x6E52736AC806831E

// computeBillingHeaderSuffixFromText is the pure suffix derivation used by
// the CLI v2.1.77+ algorithm:
//
//	chars  = text[4] + text[7] + text[20]   (default '0' if out of range)
//	suffix = sha256(salt + chars + version).hex()[:3]
//
// Decoupled from JSON parsing so callers that already hold the first-user
// message text (e.g. validator paths working from a parsed map) can reuse it.
func computeBillingHeaderSuffixFromText(text, version string) string {
	runes := []rune(text)
	var sb strings.Builder
	for _, p := range billingHeaderSuffixPositions {
		if p < len(runes) {
			sb.WriteRune(runes[p])
		} else {
			sb.WriteByte('0')
		}
	}
	sum := sha256.Sum256([]byte(billingHeaderSuffixSalt + sb.String() + version))
	return hex.EncodeToString(sum[:])[:3]
}

// computeBillingHeaderSuffix derives the cc_version suffix from a raw JSON
// body. Convenience wrapper around computeBillingHeaderSuffixFromText for
// callers that hold body bytes.
func computeBillingHeaderSuffix(body []byte, version string) string {
	return computeBillingHeaderSuffixFromText(extractFirstUserMessageText(body), version)
}

// extractFirstUserMessageText returns the user-authored text of the first
// message whose role is "user". If content is a string, returns it directly.
// If content is an array, picks the first text block whose content does not
// start with a CLI-injected wrapper prefix (see
// billingHeaderSampleSkipPrefixes) — i.e. skipping <system-reminder> and
// <local-command-*> blocks, but keeping <command-name> (user-issued slash
// commands), <session> compact summaries, and plain text. Returns "" when
// the first user message has no text blocks.
//
// Selection rule verified against capture/0521 across normal, compact, and
// /clear turns: the CLI's cc_version suffix is derived from this block.
func extractFirstUserMessageText(body []byte) string {
	msgs := gjson.GetBytes(body, "messages")
	if !msgs.Exists() || !msgs.IsArray() {
		return ""
	}
	var out string
	msgs.ForEach(func(_, msg gjson.Result) bool {
		if msg.Get("role").String() != "user" {
			return true
		}
		content := msg.Get("content")
		switch {
		case content.Type == gjson.String:
			out = stripInlinedSystemReminders(content.String())
		case content.IsArray():
			var texts []string
			content.ForEach(func(_, item gjson.Result) bool {
				if item.Get("type").String() == "text" {
					texts = append(texts, item.Get("text").String())
				}
				return true
			})
			out = pickBillingHeaderSampleText(texts)
		}
		return false // stop after first user message
	})
	return out
}

// syncBillingHeaderVersion rewrites cc_version in x-anthropic-billing-header
// system text blocks to match the version extracted from userAgent. The
// 3-char suffix is recomputed per-request from the first user message using
// the CLI's official algorithm so it stays consistent with direct-CLI wire
// fingerprints. Only touches system array blocks whose text starts with
// "x-anthropic-billing-header".
func syncBillingHeaderVersion(body []byte, userAgent string) []byte {
	version := ExtractCLIVersion(userAgent)
	if version == "" {
		return body
	}

	systemResult := gjson.GetBytes(body, "system")
	if !systemResult.Exists() || !systemResult.IsArray() {
		return body
	}

	suffix := computeBillingHeaderSuffix(body, version)
	replacement := "cc_version=" + version + "." + suffix

	idx := 0
	systemResult.ForEach(func(_, item gjson.Result) bool {
		text := item.Get("text")
		if text.Exists() && text.Type == gjson.String &&
			strings.HasPrefix(text.String(), "x-anthropic-billing-header") {
			newText := ccVersionWithSuffixRe.ReplaceAllString(text.String(), replacement)
			if newText != text.String() {
				if updated, err := sjson.SetBytes(body, fmt.Sprintf("system.%d.text", idx), newText); err == nil {
					body = updated
				}
			}
		}
		idx++
		return true
	})

	return body
}

// normalizeBillingHeaderEntrypoint forces cc_entrypoint to "cli" in any
// x-anthropic-billing-header system text block. Used for OAuth-bound traffic
// to keep a consistent entrypoint fingerprint across mimic and real-CLI
// passthrough paths, since mixed values (e.g. claude_code_sdk_python,
// vscode-ext) under the same device_id widen the upstream picture surface.
func normalizeBillingHeaderEntrypoint(body []byte) []byte {
	systemResult := gjson.GetBytes(body, "system")
	if !systemResult.Exists() || !systemResult.IsArray() {
		return body
	}

	idx := 0
	systemResult.ForEach(func(_, item gjson.Result) bool {
		text := item.Get("text")
		if text.Exists() && text.Type == gjson.String &&
			strings.HasPrefix(text.String(), "x-anthropic-billing-header") {
			newText := ccEntrypointRe.ReplaceAllString(text.String(), "${1}cli${3}")
			if newText != text.String() {
				if updated, err := sjson.SetBytes(body, fmt.Sprintf("system.%d.text", idx), newText); err == nil {
					body = updated
				}
			}
		}
		idx++
		return true
	})

	return body
}

// signBillingHeaderCCH computes the xxHash64-based CCH signature for the request
// body and replaces the cch=00000 placeholder with the computed 5-hex-char hash.
// The body must contain the placeholder when this function is called.
func signBillingHeaderCCH(body []byte) []byte {
	if !cchPlaceholderRe.Match(body) {
		return body
	}
	cch := fmt.Sprintf("%05x", xxHash64Seeded(body, cchSeed)&0xFFFFF)
	return cchPlaceholderRe.ReplaceAll(body, []byte("${1}"+cch+"${3}"))
}

// resetBillingHeaderCCH forces any existing cch=xxxxx value in the billing
// header back to the cch=00000 placeholder. Used when cch signing is disabled
// so upstream receives a uniform placeholder regardless of whether the inbound
// client was a real CLI (with its own signed cch) or a mimic client.
func resetBillingHeaderCCH(body []byte) []byte {
	if !cchAnyValueRe.Match(body) {
		return body
	}
	return cchAnyValueRe.ReplaceAll(body, []byte("${1}00000${3}"))
}

// xxHash64Seeded computes xxHash64 of data with a custom seed.
func xxHash64Seeded(data []byte, seed uint64) uint64 {
	d := xxhash.NewWithSeed(seed)
	_, _ = d.Write(data)
	return d.Sum64()
}

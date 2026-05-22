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
// skips when picking the "first user message text" that feeds the suffix
// hash. They mark blocks the CLI injects itself (environment scaffolding +
// local /-command products); the user-authored payload is whichever block
// comes after them.
//
// Verified against capture/0521 (compact + /clear scenarios): for /clear
// turns CLI samples the <command-name>/clear...</command-name> block, so
// <command-name> intentionally stays out of this skip list — only the
// strictly CLI-internal wrappers are skipped.
var billingHeaderSampleSkipPrefixes = []string{
	"<system-reminder>",
	"<local-",
}

// pickBillingHeaderSampleText selects, from text-block contents in
// messages[0].content order, the first block whose text does not start with
// any prefix in billingHeaderSampleSkipPrefixes. Falls back to the last
// entry if every block is skipped (defensive; real CLI traffic always
// leaves at least one payload block).
func pickBillingHeaderSampleText(texts []string) string {
	for _, t := range texts {
		if billingHeaderSampleTextShouldSkip(t) {
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

// inlinedSystemReminderOpenTag / inlinedSystemReminderCloseTag bracket a
// <system-reminder>...</...> wrapper that some middlewares inline into a
// single string when they flatten an array-form first user message. Used by
// stripInlinedSystemReminders to recover the user-authored payload for
// suffix derivation.
const (
	inlinedSystemReminderOpenTag  = "<system-reminder>"
	inlinedSystemReminderCloseTag = "</system-reminder>"
)

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

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
// If content is an array, returns the text of the LAST "text" block — the
// CLI prepends <system-reminder> wrapper blocks to each user turn, and the
// real user input always ends up as the final text block. Verified against
// direct CLI captures (e.g. cc_version suffix .069 requires sampling the
// trailing "你好" block, not the leading system-reminder block).
// Returns "" when the first user message has no text blocks.
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
			out = content.String()
		case content.IsArray():
			content.ForEach(func(_, item gjson.Result) bool {
				if item.Get("type").String() == "text" {
					out = item.Get("text").String()
				}
				return true // keep iterating; keep last text block
			})
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

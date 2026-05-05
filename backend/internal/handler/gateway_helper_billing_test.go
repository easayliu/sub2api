package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestClaudeCodeBodyMap_IncludesMessages 回归测试：确保从 parsedReq 构造的 bodyMap
// 携带 messages 字段。否则 validator 跑 cc_version suffix 校验时，会用空字符串
// 算 sha256 → 等同于 sample = "000"，结果只有首条用户消息恰好采样为 "000" 的
// 请求（如 ≤4 rune）才能"巧合"通过，更长的合法请求被误拒为非 Claude Code。
func TestClaudeCodeBodyMap_IncludesMessages(t *testing.T) {
	parsedReq := &service.ParsedRequest{
		Model: "claude-sonnet-4-6",
		System: []any{
			map[string]any{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
		},
		HasSystem:      true,
		MetadataUserID: "ignored",
		Messages: []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "你能做什么呢"},
				},
			},
		},
	}

	bodyMap := claudeCodeBodyMapFromParsedRequest(parsedReq)
	require.NotNil(t, bodyMap)

	msgs, ok := bodyMap["messages"].([]any)
	require.True(t, ok, "bodyMap should expose messages so validator can sample first user text")
	require.Len(t, msgs, 1)
}

// TestClaudeCodeBodyMap_OmitsEmptyMessages 验证空 messages 不会污染 bodyMap，
// 避免后续 validator 拿到无意义的空数组。
func TestClaudeCodeBodyMap_OmitsEmptyMessages(t *testing.T) {
	parsedReq := &service.ParsedRequest{
		Model:    "claude-sonnet-4-6",
		Messages: nil,
	}

	bodyMap := claudeCodeBodyMapFromParsedRequest(parsedReq)
	require.NotNil(t, bodyMap)
	_, exists := bodyMap["messages"]
	require.False(t, exists, "empty messages should be omitted entirely")
}

// TestSetClaudeCodeClientContext_LongUserMessagePassesValidator 端到端回归：
// 真 CLI UA + 完整 header + 长（≥5 rune）首条用户消息 + CLI 端算出的正确 cc_version
// 后三位，应当通过 validator 并被识别为 Claude Code 客户端。
//
// 修复前 validator 因拿不到 messages，按空字符串算 expected suffix（"000"-base），
// 与真实 CLI 用 "么00" 派生的 suffix 不一致，会把这条合法请求判成非 Claude Code。
func TestSetClaudeCodeClientContext_LongUserMessagePassesValidator(t *testing.T) {
	const cliVersion = "2.1.123"
	const userText = "你能做什么啊"        // 6 runes，sample = rune[4]+rune[7]+rune[20] = "么00"
	const correctSuffix = "d8c" // 与 capture/2.1.123 抓包及 service 层测试一致

	parsedReq := &service.ParsedRequest{
		Model: "claude-sonnet-4-6",
		System: []any{
			map[string]any{
				"type": "text",
				"text": "x-anthropic-billing-header: cc_version=" + cliVersion + "." + correctSuffix + "; cc_entrypoint=cli; cch=be2b5;",
			},
			map[string]any{
				"type": "text",
				"text": "You are Claude Code, Anthropic's official CLI for Claude.",
			},
		},
		HasSystem:      true,
		MetadataUserID: `{"device_id":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2","account_uuid":"","session_id":"12345678-1234-1234-1234-123456789abc"}`,
		Messages: []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": userText},
				},
			},
		},
	}

	bodyMap := claudeCodeBodyMapFromParsedRequest(parsedReq)
	require.NotNil(t, bodyMap)
	require.Contains(t, bodyMap, "messages")

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(""))
	req.Header.Set("User-Agent", "claude-cli/"+cliVersion+" (external, cli)")
	req.Header.Set("X-App", "cli")
	req.Header.Set("anthropic-beta", "claude-code-20250219")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
	req.Header.Set("X-Stainless-Lang", "js")
	req.Header.Set("X-Stainless-Package-Version", "0.81.0")
	req.Header.Set("X-Stainless-OS", "MacOS")

	validator := service.NewClaudeCodeValidator()
	require.True(t, validator.Validate(req, bodyMap),
		"long-message Claude Code request must pass validation now that bodyMap carries messages")
}

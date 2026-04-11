// Package claude provides constants and helpers for Claude API integration.
package claude

import "strings"

// Claude Code 客户端相关常量

// Beta header 常量
const (
	BetaOAuth               = "oauth-2025-04-20"
	BetaClaudeCode          = "claude-code-20250219"
	BetaInterleavedThinking = "interleaved-thinking-2025-05-14"
	// BetaFineGrainedToolStreaming is retained for backward compatibility
	// but is no longer sent by claude-cli/2.1.100+.
	BetaFineGrainedToolStreaming = "fine-grained-tool-streaming-2025-05-14"
	BetaTokenCounting            = "token-counting-2024-11-01"
	BetaContext1M                = "context-1m-2025-08-07"
	BetaFastMode                 = "fast-mode-2026-02-01"

	// Beta tokens observed in claude-cli/2.1.100 real traffic.
	BetaRedactThinking     = "redact-thinking-2026-02-12"
	BetaContextManagement  = "context-management-2025-06-27"
	BetaPromptCachingScope = "prompt-caching-scope-2026-01-05"
	BetaAdvisorTool        = "advisor-tool-2026-03-01"
	BetaStructuredOutputs  = "structured-outputs-2025-12-15"
	BetaAdvancedToolUse    = "advanced-tool-use-2025-11-20"
	BetaEffort             = "effort-2025-11-24"
)

// DroppedBetas 是转发时需要从 anthropic-beta header 中移除的 beta token 列表。
// 这些 token 是客户端特有的，不应透传给上游 API。
var DroppedBetas = []string{}

// DefaultBetaHeader Claude Code 客户端默认的 anthropic-beta header
// Matches claude-cli/2.1.100 real traffic for non-haiku with tools.
const DefaultBetaHeader = BetaClaudeCode + "," + BetaOAuth + "," + BetaInterleavedThinking

// MessageBetaHeaderNoTools /v1/messages 在无工具时的 beta header
//
// NOTE: Claude Code OAuth credentials are scoped to Claude Code. When we "mimic"
// Claude Code for non-Claude-Code clients, we must include the claude-code beta
// even if the request doesn't use tools, otherwise upstream may reject the
// request as a non-Claude-Code API request.
const MessageBetaHeaderNoTools = BetaClaudeCode + "," + BetaOAuth + "," + BetaInterleavedThinking

// MessageBetaHeaderWithTools /v1/messages 在有工具时的 beta header
const MessageBetaHeaderWithTools = BetaClaudeCode + "," + BetaOAuth + "," + BetaInterleavedThinking

// CountTokensBetaHeader count_tokens 请求使用的 anthropic-beta header
const CountTokensBetaHeader = BetaClaudeCode + "," + BetaOAuth + "," + BetaInterleavedThinking + "," + BetaTokenCounting

// HaikuBetaHeader Haiku 模型使用的 anthropic-beta header（不需要 claude-code beta）
const HaikuBetaHeader = BetaOAuth + "," + BetaInterleavedThinking

// APIKeyBetaHeader API-key 账号建议使用的 anthropic-beta header（不包含 oauth）
const APIKeyBetaHeader = BetaClaudeCode + "," + BetaInterleavedThinking

// APIKeyHaikuBetaHeader Haiku 模型在 API-key 账号下使用的 anthropic-beta header（不包含 oauth / claude-code）
const APIKeyHaikuBetaHeader = BetaInterleavedThinking

// MessageBetaRequestKind 描述一个 /v1/messages 请求的特征，用于动态构造
// anthropic-beta header 与真实 claude-cli/2.1.100 的抓包行为对齐。
//
// 抓包依据（capture/008, capture/011）:
//
//	haiku quota probe (max_tokens=1, no tools, no structured output):
//	  anthropic-beta = oauth, interleaved-thinking, redact-thinking,
//	                   context-management, prompt-caching-scope
//
//	haiku title gen (no tools, structured output via output_config.format):
//	  adds: advisor-tool, structured-outputs
type MessageBetaRequestKind struct {
	ModelID          string
	HasTools         bool
	HasStructuredOut bool
	// IsQuotaProbe marks the request as the startup "quota" probe (see
	// capture/008). Currently informational — the probe body carries no
	// tools and no structured output, so the default branch of
	// BuildMessageBetaTokens already yields the capture-accurate token
	// list. This field is preserved as a labelled call site so future
	// capture evidence that differentiates probe betas can be wired in
	// without adding a new parameter.
	IsQuotaProbe       bool
	IsCountTokens      bool
	IncludeClaudeCode  bool // honors legacy "non-haiku must include claude-code beta" safety claim
	IncludeOAuth       bool // true for OAuth accounts; API-key accounts set false
	IncludeTokenCounts bool // true only for count_tokens endpoint
}

// BuildMessageBetaTokens returns the ordered beta token list for a
// /v1/messages (or count_tokens) request, matching the dynamic per-request
// pattern observed in real claude-cli traffic.
//
// Token order mirrors cap 008/011 exactly so wire-level diff stays empty:
//
//	[claude-code], [oauth], interleaved-thinking, redact-thinking,
//	context-management, prompt-caching-scope,
//	[advisor-tool, structured-outputs], [token-counting]
//
// claude-code and oauth are conditional (see MessageBetaRequestKind fields).
// advisor-tool+structured-outputs are only appended for structured output
// requests, and token-counting only for count_tokens.
func BuildMessageBetaTokens(kind MessageBetaRequestKind) []string {
	isHaiku := strings.Contains(strings.ToLower(kind.ModelID), "haiku")

	tokens := make([]string, 0, 10)

	// Historical safety net: for non-haiku on OAuth, the code path has long
	// forced claude-code-20250219 to avoid upstream rejecting the request as
	// third-party ("out of extra usage"). Captured traffic for haiku shows
	// this token is NOT on the wire; we preserve the non-haiku safety claim
	// until non-haiku capture evidence allows us to drop it too.
	if kind.IncludeClaudeCode && !isHaiku {
		tokens = append(tokens, BetaClaudeCode)
	}

	if kind.IncludeOAuth {
		tokens = append(tokens, BetaOAuth)
	}

	// Core betas sent by real claude-cli on every /v1/messages (cap 008).
	tokens = append(tokens,
		BetaInterleavedThinking,
		BetaRedactThinking,
		BetaContextManagement,
		BetaPromptCachingScope,
	)

	// Structured output (output_config.format) — cap 011.
	if kind.HasStructuredOut {
		tokens = append(tokens,
			BetaAdvisorTool,
			BetaStructuredOutputs,
		)
	}

	// count_tokens endpoint needs the token-counting beta appended.
	if kind.IncludeTokenCounts {
		tokens = append(tokens, BetaTokenCounting)
	}

	return tokens
}

// DefaultHeaders 是 Claude Code 客户端默认请求头。
// Values are aligned with claude-cli/2.1.100 captured traffic. Keep these in
// sync with recent Claude CLI traffic to reduce the chance that Claude
// Code-scoped OAuth credentials are rejected as "non-CLI" usage.
var DefaultHeaders = map[string]string{
	"User-Agent":                                "claude-cli/2.1.100 (external, cli)",
	"X-Stainless-Lang":                          "js",
	"X-Stainless-Package-Version":               "0.81.0",
	"X-Stainless-OS":                            "MacOS",
	"X-Stainless-Arch":                          "arm64",
	"X-Stainless-Runtime":                       "node",
	"X-Stainless-Runtime-Version":               "v24.3.0",
	"X-Stainless-Retry-Count":                   "0",
	"X-Stainless-Timeout":                       "600",
	"X-App":                                     "cli",
	"Anthropic-Dangerous-Direct-Browser-Access": "true",
}

// Model 表示一个 Claude 模型
type Model struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

// DefaultModels Claude Code 客户端支持的默认模型列表
var DefaultModels = []Model{
	{
		ID:          "claude-opus-4-5-20251101",
		Type:        "model",
		DisplayName: "Claude Opus 4.5",
		CreatedAt:   "2025-11-01T00:00:00Z",
	},
	{
		ID:          "claude-opus-4-6",
		Type:        "model",
		DisplayName: "Claude Opus 4.6",
		CreatedAt:   "2026-02-06T00:00:00Z",
	},
	{
		ID:          "claude-sonnet-4-6",
		Type:        "model",
		DisplayName: "Claude Sonnet 4.6",
		CreatedAt:   "2026-02-18T00:00:00Z",
	},
	{
		ID:          "claude-sonnet-4-5-20250929",
		Type:        "model",
		DisplayName: "Claude Sonnet 4.5",
		CreatedAt:   "2025-09-29T00:00:00Z",
	},
	{
		ID:          "claude-haiku-4-5-20251001",
		Type:        "model",
		DisplayName: "Claude Haiku 4.5",
		CreatedAt:   "2025-10-01T00:00:00Z",
	},
}

// DefaultModelIDs 返回默认模型的 ID 列表
func DefaultModelIDs() []string {
	ids := make([]string, len(DefaultModels))
	for i, m := range DefaultModels {
		ids[i] = m.ID
	}
	return ids
}

// DefaultTestModel 测试时使用的默认模型
const DefaultTestModel = "claude-sonnet-4-5-20250929"

// ModelIDOverrides Claude OAuth 请求需要的模型 ID 映射
var ModelIDOverrides = map[string]string{
	"claude-sonnet-4-5": "claude-sonnet-4-5-20250929",
	"claude-opus-4-5":   "claude-opus-4-5-20251101",
	"claude-haiku-4-5":  "claude-haiku-4-5-20251001",
}

// ModelIDReverseOverrides 用于将上游模型 ID 还原为短名
var ModelIDReverseOverrides = map[string]string{
	"claude-sonnet-4-5-20250929": "claude-sonnet-4-5",
	"claude-opus-4-5-20251101":   "claude-opus-4-5",
	"claude-haiku-4-5-20251001":  "claude-haiku-4-5",
}

// NormalizeModelID 根据 Claude OAuth 规则映射模型
func NormalizeModelID(id string) string {
	if id == "" {
		return id
	}
	if mapped, ok := ModelIDOverrides[id]; ok {
		return mapped
	}
	return id
}

// DenormalizeModelID 将上游模型 ID 转换为短名
func DenormalizeModelID(id string) string {
	if id == "" {
		return id
	}
	if mapped, ok := ModelIDReverseOverrides[id]; ok {
		return mapped
	}
	return id
}

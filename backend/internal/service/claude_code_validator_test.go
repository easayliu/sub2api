package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestFirstSystemTextPreview(t *testing.T) {
	t.Run("nil body → missing", func(t *testing.T) {
		preview, kind, segs, runes := firstSystemTextPreview(nil, 100)
		require.Equal(t, "", preview)
		require.Equal(t, systemKindMissing, kind)
		require.Equal(t, 0, segs)
		require.Equal(t, 0, runes)
	})

	t.Run("no system field → missing", func(t *testing.T) {
		preview, kind, segs, runes := firstSystemTextPreview(map[string]any{}, 100)
		require.Equal(t, "", preview)
		require.Equal(t, systemKindMissing, kind)
		require.Equal(t, 0, segs)
		require.Equal(t, 0, runes)
	})

	t.Run("system is nil → missing", func(t *testing.T) {
		body := map[string]any{"system": nil}
		_, kind, _, _ := firstSystemTextPreview(body, 100)
		require.Equal(t, systemKindMissing, kind)
	})

	t.Run("system as string → string kind, preview from string", func(t *testing.T) {
		body := map[string]any{"system": "You are Claude Code, Anthropic's official CLI for Claude."}
		preview, kind, segs, runes := firstSystemTextPreview(body, 100)
		require.Equal(t, "You are Claude Code, Anthropic's official CLI for Claude.", preview)
		require.Equal(t, systemKindString, kind)
		require.Equal(t, 0, segs)
		require.Equal(t, len([]rune("You are Claude Code, Anthropic's official CLI for Claude.")), runes)
	})

	t.Run("system as empty array → empty_array", func(t *testing.T) {
		body := map[string]any{"system": []any{}}
		preview, kind, segs, runes := firstSystemTextPreview(body, 100)
		require.Equal(t, "", preview)
		require.Equal(t, systemKindEmptyArray, kind)
		require.Equal(t, 0, segs)
		require.Equal(t, 0, runes)
	})

	t.Run("system as array of all-empty entries → all_empty", func(t *testing.T) {
		body := map[string]any{"system": []any{
			map[string]any{"type": "text", "text": ""},
			map[string]any{"type": "text"},
		}}
		preview, kind, segs, runes := firstSystemTextPreview(body, 100)
		require.Equal(t, "", preview)
		require.Equal(t, systemKindAllEmpty, kind)
		require.Equal(t, 2, segs)
		require.Equal(t, 0, runes)
	})

	t.Run("system as wrong type (number) → wrong_type", func(t *testing.T) {
		body := map[string]any{"system": 42}
		_, kind, _, _ := firstSystemTextPreview(body, 100)
		require.Equal(t, systemKindWrongType, kind)
	})

	t.Run("system as array → array kind, first non-empty text returned", func(t *testing.T) {
		body := map[string]any{
			"system": []any{
				map[string]any{"type": "text", "text": ""},
				map[string]any{"type": "text", "text": "hello world"},
				map[string]any{"type": "text", "text": "second segment"},
			},
		}
		preview, kind, segs, runes := firstSystemTextPreview(body, 100)
		require.Equal(t, "hello world", preview)
		require.Equal(t, systemKindArray, kind)
		require.Equal(t, 3, segs)
		require.Equal(t, len([]rune("hello world")), runes)
	})

	t.Run("rune-safe truncation for multi-byte chars (array form)", func(t *testing.T) {
		body := map[string]any{
			"system": []any{
				map[string]any{"type": "text", "text": "你好世界这是一段中文文本"},
			},
		}
		preview, kind, segs, runes := firstSystemTextPreview(body, 5)
		require.Equal(t, "你好世界这", preview)
		require.Equal(t, systemKindArray, kind)
		require.Equal(t, 1, segs)
		require.Equal(t, 12, runes)
	})

	t.Run("rune-safe truncation for multi-byte chars (string form)", func(t *testing.T) {
		body := map[string]any{"system": "你好世界这是一段中文文本"}
		preview, kind, _, runes := firstSystemTextPreview(body, 5)
		require.Equal(t, "你好世界这", preview)
		require.Equal(t, systemKindString, kind)
		require.Equal(t, 12, runes)
	})

	t.Run("newlines replaced with sentinel", func(t *testing.T) {
		body := map[string]any{
			"system": []any{
				map[string]any{"type": "text", "text": "line1\nline2\r\nline3"},
			},
		}
		preview, _, _, _ := firstSystemTextPreview(body, 100)
		require.False(t, strings.ContainsAny(preview, "\r\n"))
		require.Contains(t, preview, "line1⏎line2⏎⏎line3")
	})
}

func TestClaudeCodeValidator_ProbeBypass(t *testing.T) {
	validator := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages", nil)
	req.Header.Set("User-Agent", "claude-cli/1.2.3 (darwin; arm64)")
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.IsMaxTokensOneHaikuRequest, true))

	ok := validator.Validate(req, map[string]any{
		"model":      "claude-haiku-4-5",
		"max_tokens": 1,
	})
	require.True(t, ok)
}

func TestClaudeCodeValidator_ProbeBypassRequiresUA(t *testing.T) {
	validator := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages", nil)
	req.Header.Set("User-Agent", "curl/8.0.0")
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.IsMaxTokensOneHaikuRequest, true))

	ok := validator.Validate(req, map[string]any{
		"model":      "claude-haiku-4-5",
		"max_tokens": 1,
	})
	require.False(t, ok)
}

func TestClaudeCodeValidator_MessagesWithoutProbeStillNeedStrictValidation(t *testing.T) {
	validator := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages", nil)
	req.Header.Set("User-Agent", "claude-cli/1.2.3 (darwin; arm64)")

	ok := validator.Validate(req, map[string]any{
		"model":      "claude-haiku-4-5",
		"max_tokens": 1,
	})
	require.False(t, ok)
}

func TestClaudeCodeValidator_NonMessagesPathUAOnly(t *testing.T) {
	validator := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/models", nil)
	req.Header.Set("User-Agent", "claude-cli/1.2.3 (darwin; arm64)")

	ok := validator.Validate(req, nil)
	require.True(t, ok)
}

// count_tokens requests from the CLI do not carry a system prompt or
// metadata.user_id, so strict Step 4 validation must be skipped once the
// User-Agent has already proven the caller is a real CLI.
func TestClaudeCodeValidator_CountTokensBypassWithUA(t *testing.T) {
	validator := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages/count_tokens", nil)
	req.Header.Set("User-Agent", "claude-cli/1.2.3 (darwin; arm64)")

	ok := validator.Validate(req, map[string]any{
		"model": "claude-opus-4-7",
	})
	require.True(t, ok)
}

func TestClaudeCodeValidator_CountTokensRequiresUA(t *testing.T) {
	validator := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages/count_tokens", nil)
	req.Header.Set("User-Agent", "curl/8.0.0")

	ok := validator.Validate(req, map[string]any{
		"model": "claude-opus-4-7",
	})
	require.False(t, ok)
}

func TestExtractVersion(t *testing.T) {
	v := NewClaudeCodeValidator()
	tests := []struct {
		ua   string
		want string
	}{
		{"claude-cli/2.1.22 (darwin; arm64)", "2.1.22"},
		{"claude-cli/1.0.0", "1.0.0"},
		{"Claude-CLI/3.10.5 (linux; x86_64)", "3.10.5"}, // 大小写不敏感
		{"curl/8.0.0", ""},                              // 非 Claude CLI
		{"", ""},                                        // 空字符串
		{"claude-cli/", ""},                             // 无版本号
		{"claude-cli/2.1.22-beta", "2.1.22"},            // 带后缀仍提取主版本号
	}
	for _, tt := range tests {
		got := v.ExtractVersion(tt.ua)
		require.Equal(t, tt.want, got, "ExtractVersion(%q)", tt.ua)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"2.1.0", "2.1.0", 0},   // 相等
		{"2.1.1", "2.1.0", 1},   // patch 更大
		{"2.0.0", "2.1.0", -1},  // minor 更小
		{"3.0.0", "2.99.99", 1}, // major 更大
		{"1.0.0", "2.0.0", -1},  // major 更小
		{"0.0.1", "0.0.0", 1},   // patch 差异
		{"", "1.0.0", -1},       // 空字符串 vs 正常版本
		{"v2.1.0", "2.1.0", 0},  // v 前缀处理
	}
	for _, tt := range tests {
		got := CompareVersions(tt.a, tt.b)
		require.Equal(t, tt.want, got, "CompareVersions(%q, %q)", tt.a, tt.b)
	}
}

func TestSetGetClaudeCodeVersion(t *testing.T) {
	ctx := context.Background()
	require.Equal(t, "", GetClaudeCodeVersion(ctx), "empty context should return empty string")

	ctx = SetClaudeCodeVersion(ctx, "2.1.63")
	require.Equal(t, "2.1.63", GetClaudeCodeVersion(ctx))
}

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

// shapeMap turns the buildRejectShape return slice into a map for easier
// per-key assertions in tests.
func shapeMap(t *testing.T, kv []any) map[string]any {
	t.Helper()
	require.Equal(t, 0, len(kv)%2, "shape kv list must have even length")
	out := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		key, ok := kv[i].(string)
		require.True(t, ok, "shape key at index %d is not string", i)
		out[key] = kv[i+1]
	}
	return out
}

func TestBuildRejectShape(t *testing.T) {
	t.Run("nil request", func(t *testing.T) {
		require.Nil(t, buildRejectShape(nil, nil))
	})

	t.Run("empty request, nil body → all defaults", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		shape := shapeMap(t, buildRejectShape(req, nil))

		require.Equal(t, "", shape["shape_ua_version"])
		require.Equal(t, false, shape["shape_ua_external"])
		require.Equal(t, "", shape["shape_x_app"])
		require.Equal(t, "", shape["shape_anthropic_version"])
		require.Equal(t, 0, shape["shape_beta_tokens"])
		require.Equal(t, false, shape["shape_has_cc_beta_token"])
		require.Equal(t, false, shape["shape_has_dangerous_direct_browser"])
		require.Equal(t, "", shape["shape_x_stainless_lang"])
		require.Equal(t, "", shape["shape_x_stainless_os"])
		require.Equal(t, false, shape["shape_x_stainless_pkg_present"])
		require.Equal(t, systemKindMissing, shape["shape_system_kind"])
		require.Equal(t, 0, shape["shape_system_segments"])
		require.Equal(t, false, shape["shape_has_billing_header"])
		require.Equal(t, false, shape["shape_has_env_block"])
		require.Equal(t, metadataKindMissing, shape["shape_metadata_kind"])
		require.Equal(t, false, shape["shape_has_metadata_user_id"])
		require.Equal(t, metadataUserIDMissing, shape["shape_metadata_user_id_format"])
	})

	t.Run("real CLI request → all positives", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		req.Header.Set("User-Agent", "claude-cli/2.1.123 (external, cli)")
		req.Header.Set("X-App", "cli")
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14")
		req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
		req.Header.Set("X-Stainless-Lang", "js")
		req.Header.Set("X-Stainless-OS", "MacOS")
		req.Header.Set("X-Stainless-Package-Version", "0.81.0")

		body := map[string]any{
			"system": []any{
				map[string]any{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.123.d8c;"},
				map[string]any{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
				map[string]any{"type": "text", "text": "You have been invoked in the following environment:\n- Platform: darwin\n- OS Version: Darwin 25.0\n- Shell: zsh"},
			},
			"metadata": map[string]any{
				"user_id": `{"device_id":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2","account_uuid":"","session_id":"12345678-1234-1234-1234-123456789abc"}`,
			},
		}

		shape := shapeMap(t, buildRejectShape(req, body))

		require.Equal(t, "2.1.123", shape["shape_ua_version"])
		require.Equal(t, true, shape["shape_ua_external"])
		require.Equal(t, "cli", shape["shape_x_app"])
		require.Equal(t, "2023-06-01", shape["shape_anthropic_version"])
		require.Equal(t, 3, shape["shape_beta_tokens"])
		require.Equal(t, true, shape["shape_has_cc_beta_token"])
		require.Equal(t, true, shape["shape_has_dangerous_direct_browser"])
		require.Equal(t, "js", shape["shape_x_stainless_lang"])
		require.Equal(t, "MacOS", shape["shape_x_stainless_os"])
		require.Equal(t, true, shape["shape_x_stainless_pkg_present"])
		require.Equal(t, systemKindArray, shape["shape_system_kind"])
		require.Equal(t, 3, shape["shape_system_segments"])
		require.Equal(t, true, shape["shape_has_billing_header"])
		require.Equal(t, true, shape["shape_has_env_block"])
		require.Equal(t, metadataKindPresent, shape["shape_metadata_kind"])
		require.Equal(t, true, shape["shape_has_metadata_user_id"])
		require.Equal(t, metadataUserIDJSON, shape["shape_metadata_user_id_format"])
	})

	t.Run("legacy metadata format detected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		body := map[string]any{
			"metadata": map[string]any{
				"user_id": "user_a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2_account__session_12345678-1234-1234-1234-123456789abc",
			},
		}
		shape := shapeMap(t, buildRejectShape(req, body))
		require.Equal(t, metadataUserIDLegacy, shape["shape_metadata_user_id_format"])
	})

	t.Run("invalid metadata format detected", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		body := map[string]any{
			"metadata": map[string]any{"user_id": "garbage"},
		}
		shape := shapeMap(t, buildRejectShape(req, body))
		require.Equal(t, metadataUserIDInvalid, shape["shape_metadata_user_id_format"])
	})

	t.Run("metadata wrong type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		body := map[string]any{"metadata": "not-a-map"}
		shape := shapeMap(t, buildRejectShape(req, body))
		require.Equal(t, metadataKindWrongType, shape["shape_metadata_kind"])
		require.Equal(t, false, shape["shape_has_metadata_user_id"])
	})

	t.Run("ua external suffix detection (case insensitive, whitespace)", func(t *testing.T) {
		cases := map[string]bool{
			"claude-cli/2.1.92 (external, cli)":  true,
			"claude-cli/2.1.92 (external,cli)":   true,
			"Claude-CLI/2.1.92 (External, CLI)":  true,
			"claude-cli/2.1.92":                  false,
			"claude-cli/2.1.92 (darwin; arm64)":  false,
			"":                                   false,
		}
		for ua, want := range cases {
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			req.Header.Set("User-Agent", ua)
			shape := shapeMap(t, buildRejectShape(req, nil))
			require.Equal(t, want, shape["shape_ua_external"], "UA: %q", ua)
		}
	})

	t.Run("beta token count handles whitespace and empties", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		req.Header.Set("anthropic-beta", " a , ,b,, c ")
		shape := shapeMap(t, buildRejectShape(req, nil))
		require.Equal(t, 3, shape["shape_beta_tokens"])
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

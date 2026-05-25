package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/assert"
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
			"claude-cli/2.1.92 (external, cli)": true,
			"claude-cli/2.1.92 (external,cli)":  true,
			"Claude-CLI/2.1.92 (External, CLI)": true,
			"claude-cli/2.1.92":                 false,
			"claude-cli/2.1.92 (darwin; arm64)": false,
			"":                                  false,
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
	req.Header.Set("User-Agent", "claude-cli/1.2.3 (external, cli)")
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

// CC issues haiku title-generation requests with output_config.format.type
// == "json_schema"; they drop claude-code-20250219 from anthropic-beta which
// would otherwise fail 4.2. Step 3.6 bypasses these once UA matches.
// Shape based on capture/011_215059_api.anthropic.com:443_v1_messages?beta=true.json.
func TestClaudeCodeValidator_HaikuTitleGenBypass(t *testing.T) {
	validator := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages", nil)
	req.Header.Set("User-Agent", "claude-cli/2.1.100 (external, cli)")

	ok := validator.Validate(req, map[string]any{
		"model":      "claude-haiku-4-5-20251001",
		"max_tokens": 32000,
		"output_config": map[string]any{
			"format": map[string]any{
				"type": "json_schema",
				"schema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"title": map[string]any{"type": "string"}},
					"required":   []any{"title"},
				},
			},
		},
	})
	require.True(t, ok)
}

// Bypass requires UA to match the CLI pattern — Step 1 still gates.
func TestClaudeCodeValidator_HaikuTitleGenBypassRequiresUA(t *testing.T) {
	validator := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages", nil)
	req.Header.Set("User-Agent", "curl/8.0.0")

	ok := validator.Validate(req, map[string]any{
		"model": "claude-haiku-4-5-20251001",
		"output_config": map[string]any{
			"format": map[string]any{"type": "json_schema"},
		},
	})
	require.False(t, ok)
}

// output_config present but format.type != "json_schema" (e.g. only an effort
// hint) is not the title-gen fingerprint and must still go through Step 4.
func TestClaudeCodeValidator_HaikuOutputConfigWithoutJSONSchemaStillStrict(t *testing.T) {
	validator := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages", nil)
	req.Header.Set("User-Agent", "claude-cli/2.1.100 (external, cli)")

	ok := validator.Validate(req, map[string]any{
		"model": "claude-haiku-4-5-20251001",
		"output_config": map[string]any{
			"format": map[string]any{"type": "text"},
		},
	})
	require.False(t, ok)
}

// A user choosing haiku as their primary model (no output_config) must still
// pass strict Step 4 validation — guards against false-bypassing real haiku
// chats (see capture/0508/019_*.json).
func TestClaudeCodeValidator_HaikuWithoutOutputConfigStillStrict(t *testing.T) {
	validator := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages", nil)
	req.Header.Set("User-Agent", "claude-cli/2.1.133 (external, cli)")

	ok := validator.Validate(req, map[string]any{
		"model":      "claude-haiku-4-5-20251001",
		"max_tokens": 32000,
	})
	require.False(t, ok)
}

func TestClaudeCodeValidator_MessagesWithoutProbeStillNeedStrictValidation(t *testing.T) {
	validator := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages", nil)
	req.Header.Set("User-Agent", "claude-cli/1.2.3 (external, cli)")

	ok := validator.Validate(req, map[string]any{
		"model":      "claude-haiku-4-5",
		"max_tokens": 1,
	})
	require.False(t, ok)
}

func TestClaudeCodeValidator_NonMessagesPathUAOnly(t *testing.T) {
	validator := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/models", nil)
	req.Header.Set("User-Agent", "claude-cli/1.2.3 (external, cli)")

	ok := validator.Validate(req, nil)
	require.True(t, ok)
}

// count_tokens requests from the CLI do not carry a system prompt or
// metadata.user_id, so strict Step 4 validation must be skipped once the
// User-Agent has already proven the caller is a real CLI.
func TestClaudeCodeValidator_CountTokensBypassWithUA(t *testing.T) {
	validator := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages/count_tokens", nil)
	req.Header.Set("User-Agent", "claude-cli/1.2.3 (external, cli)")

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

// nilSettingsProvider 是一个永远 panic 的 provider，用于断言 typed-nil 守卫
// 是否真的拦截了底层调用。任何尝试调用其方法的代码都会被 panic 暴露。
type nilSettingsProvider struct{}

func (*nilSettingsProvider) IsStrictCCVersionEnabled(context.Context) bool {
	panic("IsStrictCCVersionEnabled must not be called on a typed-nil provider")
}

type fixedSettingsProvider struct{ enabled bool }

func (p *fixedSettingsProvider) IsStrictCCVersionEnabled(context.Context) bool {
	return p.enabled
}

func TestClaudeCodeValidator_StrictCCVersionEnabled_DefaultsTrue(t *testing.T) {
	v := NewClaudeCodeValidator()
	require.True(t, v.strictCCVersionEnabled(context.Background()),
		"未注入 provider 时应维持历史 strict 行为")
}

func TestClaudeCodeValidator_SetStrictCCVersionSettings_UntypedNil(t *testing.T) {
	v := NewClaudeCodeValidator()
	v.SetStrictCCVersionSettings(nil)
	require.True(t, v.strictCCVersionEnabled(context.Background()),
		"显式传无类型 nil 应回退到默认 strict")
}

func TestClaudeCodeValidator_SetStrictCCVersionSettings_TypedNilPointer(t *testing.T) {
	v := NewClaudeCodeValidator()
	var p *nilSettingsProvider // typed-nil：未通过守卫会在 strictCCVersionEnabled 中 panic
	v.SetStrictCCVersionSettings(p)
	require.NotPanics(t, func() {
		require.True(t, v.strictCCVersionEnabled(context.Background()),
			"typed-nil provider 必须被守卫识别为未注入并返回默认 strict")
	})
}

func TestIsAllowedClaudeCLIUAFamily(t *testing.T) {
	allowed := []string{
		"claude-cli/2.1.146 (external, cli)",
		"claude-cli/2.1.109 (external, sdk-cli)",
		"claude-cli/2.1.100 (external,cli)",                               // missing space — tolerated
		"Claude-CLI/2.1.92 (External, CLI)",                               // case insensitive
		"claude-cli/2.1.146 (external, cli) ",                             // trailing whitespace allowed
		"claude-cli/2.1.146 (external, sdk-cli)\n",                        // trailing newline ok
		"claude-cli/2.1.145 (external, claude-vscode)",                    // VSCode 截断形态
		"claude-cli/2.1.145 (external, claude-vscode, agent-sdk/0.3.145)", // VSCode + agent-sdk
		"claude-cli/2.1.144 (external, claude-vscode, agent-sdk/0.3.144)",
		"Claude-CLI/2.1.144 (External, Claude-VSCode, Agent-SDK/0.3.144)", // case insensitive
	}
	for _, ua := range allowed {
		require.True(t, isAllowedClaudeCLIUAFamily(ua), "should allow: %q", ua)
	}

	denied := []string{
		"",
		"claude-cli/2.1.146", // no parenthesized suffix
		"claude-cli/2.1.128 (external, local-agent)",                          // illegitimate
		"claude-cli/2.1.142 (external, claude-desktop-3p, agent-sdk/0.3.142)", // Desktop 3p
		"claude-cli/2.1.146 (darwin; arm64)",                                  // pre-2.1.77 style
		"claude-cli/2.1.146 (external, cli, extra-token)",                     // extra tokens not allowed
		"claude-cli/2.1.146 (external, sdkcli)",                               // typo / forged
		"claude-cli/2.1.146 (external, claude-vscode-fake)",                   // forged claude-vscode-like prefix
		"claude-cli/2.1.146 (external, claude-vscode, junk-suffix)",           // claude-vscode followed by non-agent-sdk token
		"curl/8.0.0",
	}
	for _, ua := range denied {
		require.False(t, isAllowedClaudeCLIUAFamily(ua), "should deny: %q", ua)
	}
}

func TestClaudeCodeValidator_ValidateUserAgent_EnforcesFamily(t *testing.T) {
	v := NewClaudeCodeValidator()
	// Modern allow-listed families pass.
	require.True(t, v.ValidateUserAgent("claude-cli/2.1.146 (external, cli)"))
	require.True(t, v.ValidateUserAgent("claude-cli/2.1.109 (external, sdk-cli)"))
	require.True(t, v.ValidateUserAgent("claude-cli/2.1.145 (external, claude-vscode)"))
	require.True(t, v.ValidateUserAgent("claude-cli/2.1.145 (external, claude-vscode, agent-sdk/0.3.145)"))
	// Prefix-only is no longer enough — family must also match.
	require.False(t, v.ValidateUserAgent("claude-cli/2.1.146"))
	require.False(t, v.ValidateUserAgent("claude-cli/2.1.128 (external, local-agent)"))
	require.False(t, v.ValidateUserAgent("claude-cli/2.1.142 (external, claude-desktop-3p, agent-sdk/0.3.142)"))
}

func TestClaudeCodeValidator_Validate_RejectsNonAllowedFamily(t *testing.T) {
	v := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages", nil)
	req.Header.Set("User-Agent", "claude-cli/2.1.128 (external, local-agent)")
	// Even on a non-messages path, family-not-allowed should fail.
	require.False(t, v.Validate(req, map[string]any{}))
}

func TestRequestIDAttrs(t *testing.T) {
	t.Run("nil request returns empty values", func(t *testing.T) {
		attrs := requestIDAttrs(nil)
		require.Equal(t, []any{"request_id", "", "client_request_id", ""}, attrs)
	})

	t.Run("empty context returns empty strings", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		attrs := requestIDAttrs(req)
		require.Equal(t, []any{"request_id", "", "client_request_id", ""}, attrs)
	})

	t.Run("ids from context are emitted", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		ctx := context.WithValue(req.Context(), ctxkey.RequestID, "req-abc")
		ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "client-xyz")
		req = req.WithContext(ctx)
		attrs := requestIDAttrs(req)
		require.Equal(t, []any{"request_id", "req-abc", "client_request_id", "client-xyz"}, attrs)
	})
}

func TestClaudeCodeValidator_Validate_NonMessagesPathStillNeedsAllowedFamily(t *testing.T) {
	v := NewClaudeCodeValidator()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/models", nil)
	// Allow-listed families pass on non-messages paths.
	for _, ua := range []string{
		"claude-cli/2.1.146 (external, cli)",
		"claude-cli/2.1.145 (external, claude-vscode, agent-sdk/0.3.145)",
	} {
		req.Header.Set("User-Agent", ua)
		require.True(t, v.Validate(req, nil), "ua=%q", ua)
	}

	// Illegitimate family fails at Step 1.5 even on non-messages path.
	req.Header.Set("User-Agent", "claude-cli/2.1.128 (external, local-agent)")
	require.False(t, v.Validate(req, nil))
}

func TestClaudeCodeValidator_SetStrictCCVersionSettings_RealProviderRespectsValue(t *testing.T) {
	v := NewClaudeCodeValidator()
	v.SetStrictCCVersionSettings(&fixedSettingsProvider{enabled: false})
	require.False(t, v.strictCCVersionEnabled(context.Background()),
		"真实 provider 返回 false 时应被尊重，跳过 Step 4.4 的 suffix 比对")

	v.SetStrictCCVersionSettings(&fixedSettingsProvider{enabled: true})
	require.True(t, v.strictCCVersionEnabled(context.Background()),
		"provider 替换后新值应即时生效")
}

// helper: build a request with the given UA and a body that already passes
// the pre-suffix Step 4.4 checks (billing header parses, version matches UA).
func newBillingSuffixTestReq(t *testing.T, ua, billingText, firstUserText string) (*http.Request, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages", nil)
	req.Header.Set("User-Agent", ua)
	body := map[string]any{
		"system": []any{
			map[string]any{"type": "text", "text": billingText},
		},
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": firstUserText,
			},
		},
	}
	return req, body
}

func TestValidateBillingHeaderSuffix_PreSuffixChecksAlwaysEnforced(t *testing.T) {
	// All 4 pre-suffix branches must reject regardless of strict toggle —
	// they catch the weakest forgeries (no header at all, garbled cc_version,
	// version-UA mismatch).
	for _, strict := range []bool{true, false} {
		strict := strict
		t.Run("billing_header_missing always rejects strict="+boolStr(strict), func(t *testing.T) {
			v := NewClaudeCodeValidator()
			v.SetStrictCCVersionSettings(&fixedSettingsProvider{enabled: strict})
			req := httptest.NewRequest(http.MethodPost, "http://example.com/v1/messages", nil)
			req.Header.Set("User-Agent", "claude-cli/2.1.100 (external, cli)")
			body := map[string]any{
				"system":   []any{},
				"messages": []any{map[string]any{"role": "user", "content": "hi"}},
			}
			require.False(t, v.validateBillingHeaderSuffix(req, body))
		})

		t.Run("cc_version_unparseable always rejects strict="+boolStr(strict), func(t *testing.T) {
			v := NewClaudeCodeValidator()
			v.SetStrictCCVersionSettings(&fixedSettingsProvider{enabled: strict})
			req, body := newBillingSuffixTestReq(t,
				"claude-cli/2.1.100 (external, cli)",
				"x-anthropic-billing-header: cc_version=2.1.100; cc_entrypoint=cli; cch=00000;", // no .SSS
				"hi",
			)
			require.False(t, v.validateBillingHeaderSuffix(req, body))
		})

		t.Run("version_ua_mismatch always rejects strict="+boolStr(strict), func(t *testing.T) {
			v := NewClaudeCodeValidator()
			v.SetStrictCCVersionSettings(&fixedSettingsProvider{enabled: strict})
			req, body := newBillingSuffixTestReq(t,
				"claude-cli/2.1.100 (external, cli)",
				"x-anthropic-billing-header: cc_version=2.1.110.abc; cc_entrypoint=cli; cch=00000;", // 110 != 100
				"hi",
			)
			require.False(t, v.validateBillingHeaderSuffix(req, body))
		})
	}
}

func TestValidateBillingHeaderSuffix_SuffixBranchControlledByToggle(t *testing.T) {
	// Build a request with deliberately wrong (but valid-hex) suffix — accept
	// iff strict off. For "hello world test" + ver=2.1.100 the correct suffix
	// is 714; we send 000 instead so the branch fires.
	mkReq := func() (*http.Request, map[string]any) {
		return newBillingSuffixTestReq(t,
			"claude-cli/2.1.100 (external, cli)",
			"x-anthropic-billing-header: cc_version=2.1.100.000; cc_entrypoint=cli; cch=00000;",
			"hello world test",
		)
	}

	t.Run("strict ON: wrong suffix rejects", func(t *testing.T) {
		v := NewClaudeCodeValidator()
		v.SetStrictCCVersionSettings(&fixedSettingsProvider{enabled: true})
		req, body := mkReq()
		require.False(t, v.validateBillingHeaderSuffix(req, body))
	})

	t.Run("strict OFF: wrong suffix passes", func(t *testing.T) {
		v := NewClaudeCodeValidator()
		v.SetStrictCCVersionSettings(&fixedSettingsProvider{enabled: false})
		req, body := mkReq()
		require.True(t, v.validateBillingHeaderSuffix(req, body))
	})
}

func TestValidateBillingHeaderSuffix_CorrectSuffixAlwaysPasses(t *testing.T) {
	// Reference 2.1.77 example with correct suffix b88 — should pass under
	// both strict modes.
	mkReq := func() (*http.Request, map[string]any) {
		return newBillingSuffixTestReq(t,
			"claude-cli/2.1.77 (external, cli)",
			"x-anthropic-billing-header: cc_version=2.1.77.b88; cc_entrypoint=cli; cch=00000;",
			"Hello, how are you?", // chars at [4,7,20] = 'o','h','0' -> b88 per spec
		)
	}

	for _, strict := range []bool{true, false} {
		strict := strict
		t.Run("strict="+boolStr(strict), func(t *testing.T) {
			v := NewClaudeCodeValidator()
			v.SetStrictCCVersionSettings(&fixedSettingsProvider{enabled: strict})
			req, body := mkReq()
			require.True(t, v.validateBillingHeaderSuffix(req, body))
		})
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestDescribeCompactAnchorsInMsg0(t *testing.T) {
	t.Run("nil body returns no_msg0", func(t *testing.T) {
		assert.Equal(t, "no_msg0", describeCompactAnchorsInMsg0(nil))
	})

	t.Run("missing messages returns no_msg0", func(t *testing.T) {
		assert.Equal(t, "no_msg0",
			describeCompactAnchorsInMsg0(map[string]any{}))
	})

	t.Run("string content with anchor", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role":    "user",
					"content": "Called X.\nThis session is being continued from a previous",
				},
			},
		}
		got := describeCompactAnchorsInMsg0(body)
		assert.Equal(t, "string:[10]", got)
	})

	t.Run("string content without anchor", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": "Called X with args"},
			},
		}
		got := describeCompactAnchorsInMsg0(body)
		assert.Equal(t, "string:[]", got)
	})

	t.Run("string content with SR inners", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": "<system-reminder>" +
						strings.Repeat("X", 30) + "This session is being continued from prev" +
						"</system-reminder>",
				},
			},
		}
		got := describeCompactAnchorsInMsg0(body)
		// string-level anchor offset + inner[0] offset
		require.Contains(t, got, "string:[")
		require.Contains(t, got, "inner[0]:[")
	})

	t.Run("array content with per-block anchors", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": "<system-reminder>foo</system-reminder>"},
						map[string]any{"type": "text", "text": "This session is being continued from prev"},
						map[string]any{"type": "text", "text": "user input"},
					},
				},
			},
		}
		got := describeCompactAnchorsInMsg0(body)
		require.Contains(t, got, "blk[0]:[]")
		require.Contains(t, got, "blk[1]:[0]")
		require.Contains(t, got, "blk[2]:[]")
	})
}

func TestExtractFirstUserMessageTextFromMap(t *testing.T) {
	t.Run("nil body returns empty", func(t *testing.T) {
		require.Equal(t, "", extractFirstUserMessageTextFromMap(nil))
	})

	t.Run("messages missing returns empty", func(t *testing.T) {
		require.Equal(t, "", extractFirstUserMessageTextFromMap(map[string]any{}))
	})

	t.Run("string content returned directly", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": "hello"},
			},
		}
		require.Equal(t, "hello", extractFirstUserMessageTextFromMap(body))
	})

	t.Run("skips system-reminder, returns first non-skipped block", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": "<system-reminder>x</system-reminder>"},
						map[string]any{"type": "text", "text": "real input"},
					},
				},
			},
		}
		require.Equal(t, "real input", extractFirstUserMessageTextFromMap(body))
	})

	t.Run("/clear (empty stdout): samples <command-name>/clear", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": "<system-reminder>a</system-reminder>"},
						map[string]any{"type": "text", "text": "<system-reminder>b</system-reminder>"},
						map[string]any{"type": "text", "text": "<system-reminder>c</system-reminder>"},
						map[string]any{"type": "text", "text": "<system-reminder>d</system-reminder>"},
						map[string]any{"type": "text", "text": "<local-command-caveat>caveat</local-command-caveat>"},
						map[string]any{"type": "text", "text": "<command-name>/clear</command-name>"},
						map[string]any{"type": "text", "text": "<local-command-stdout></local-command-stdout>"},
						map[string]any{"type": "text", "text": "nihao"},
					},
				},
			},
		}
		require.Equal(t,
			"<command-name>/clear</command-name>",
			extractFirstUserMessageTextFromMap(body))
	})

	t.Run("/mcp (non-empty stdout): skips <command-name>, samples user input", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": "<system-reminder>a</system-reminder>"},
						map[string]any{"type": "text", "text": "<command-name>/mcp</command-name>"},
						map[string]any{"type": "text", "text": "<local-command-stdout>MCP status output</local-command-stdout>"},
						map[string]any{"type": "text", "text": "https://example.com"},
					},
				},
			},
		}
		require.Equal(t,
			"https://example.com",
			extractFirstUserMessageTextFromMap(body))
	})

	t.Run("compact next turn: samples compact summary block, not user input", func(t *testing.T) {
		// Mirrors capture/0521/014 / 028 / 040.
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": "<system-reminder>a</system-reminder>"},
						map[string]any{"type": "text", "text": "<system-reminder>b</system-reminder>"},
						map[string]any{"type": "text", "text": "<system-reminder>c</system-reminder>"},
						map[string]any{"type": "text", "text": "This session is being continued..."},
						map[string]any{"type": "text", "text": "<local-command-caveat>caveat</local-command-caveat>"},
						map[string]any{"type": "text", "text": "<command-name>/compact</command-name>"},
						map[string]any{"type": "text", "text": "<local-command-stdout>Compacted</local-command-stdout>"},
						map[string]any{"type": "text", "text": "nihaowe"},
					},
				},
			},
		}
		require.Equal(t,
			"This session is being continued...",
			extractFirstUserMessageTextFromMap(body))
	})

	t.Run("skips non-user messages", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{"role": "assistant", "content": "ignored"},
				map[string]any{"role": "user", "content": "real"},
			},
		}
		require.Equal(t, "real", extractFirstUserMessageTextFromMap(body))
	})
}

func TestDescribeMsg0ContentBlocks(t *testing.T) {
	t.Run("nil body returns empty", func(t *testing.T) {
		require.Equal(t, "", describeMsg0ContentBlocks(nil))
	})

	t.Run("missing messages returns no_messages", func(t *testing.T) {
		require.Equal(t, "no_messages", describeMsg0ContentBlocks(map[string]any{}))
	})

	t.Run("empty messages returns no_messages", func(t *testing.T) {
		body := map[string]any{"messages": []any{}}
		require.Equal(t, "no_messages", describeMsg0ContentBlocks(body))
	})

	t.Run("msg[0] not an object", func(t *testing.T) {
		body := map[string]any{"messages": []any{"plain string"}}
		require.Equal(t, "msg0_non_object", describeMsg0ContentBlocks(body))
	})

	t.Run("msg[0] without content field", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{map[string]any{"role": "user"}},
		}
		require.Equal(t, "no_content", describeMsg0ContentBlocks(body))
	})

	t.Run("msg[0].content is null", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": nil},
			},
		}
		require.Equal(t, "content_null", describeMsg0ContentBlocks(body))
	})

	t.Run("short string content - head only, no tail (would overlap)", func(t *testing.T) {
		// 28 runes <= 2 * msg0BlockHeadRunes (48) → no tail emitted.
		body := map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": "hello world this is a string"},
			},
		}
		got := describeMsg0ContentBlocks(body)
		require.Equal(t, "content_string:len=28:head=hello world this is a st", got)
	})

	t.Run("long string content emits both head and tail", func(t *testing.T) {
		// 24 'a' + 10 '-' + 24 'b' = 58 runes > 2*24, so head=24 'a' and
		// tail=24 'b' are both shown.
		body := map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": strings.Repeat("a", 24) + strings.Repeat("-", 10) + strings.Repeat("b", 24)},
			},
		}
		got := describeMsg0ContentBlocks(body)
		require.Equal(t,
			"content_string:len=58:head="+strings.Repeat("a", 24)+":tail="+strings.Repeat("b", 24),
			got)
	})

	t.Run("string boundary case at 2*msg0BlockHeadRunes - no tail", func(t *testing.T) {
		// len == 48 == 2*24 → still no tail (strictly greater required).
		body := map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": strings.Repeat("x", 2*msg0BlockHeadRunes)},
			},
		}
		got := describeMsg0ContentBlocks(body)
		require.Equal(t,
			"content_string:len="+strconv.Itoa(2*msg0BlockHeadRunes)+":head="+strings.Repeat("x", msg0BlockHeadRunes),
			got)
	})

	t.Run("tail reveals close-tag-at-end forgery (whole content wrapped in single SR)", func(t *testing.T) {
		// Simulates the suspected forge body where everything is wrapped in
		// one <system-reminder>...</system-reminder>, including the trailing
		// real text. tail surfaces the close tag at the very end.
		content := "<system-reminder>\n" + strings.Repeat("Called something\n", 5) + "This session is being continued from a previous conversation</system-reminder>"
		body := map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": content},
			},
		}
		got := describeMsg0ContentBlocks(body)
		require.Contains(t, got, "head=<system-reminder>⏎Called")
		require.Contains(t, got, "tail=")
		require.Contains(t, got, "</system-reminder>")
	})

	t.Run("string content with newline collapses to ⏎", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": "line1\nline2"},
			},
		}
		got := describeMsg0ContentBlocks(body)
		require.Contains(t, got, "head=line1⏎line2")
	})

	t.Run("content as int renders content_wrong_type", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": 42},
			},
		}
		got := describeMsg0ContentBlocks(body)
		require.Equal(t, "content_wrong_type:int", got)
	})

	t.Run("empty array content renders content_empty_array", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": []any{}},
			},
		}
		require.Equal(t, "content_empty_array", describeMsg0ContentBlocks(body))
	})

	t.Run("renders text blocks with len + truncated head", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": "<system-reminder>\nfoo\n</system-reminder>"},
						map[string]any{"type": "text", "text": "<command-name>/clear</command-name>"},
						map[string]any{"type": "text", "text": "hi"},
					},
				},
			},
		}
		got := describeMsg0ContentBlocks(body)
		require.Contains(t, got, "0:text:len=40:head=<system-reminder>⏎foo⏎</")
		require.Contains(t, got, "1:text:len=35:head=<command-name>/clear</co")
		require.Contains(t, got, "2:text:len=2:head=hi")
		require.Contains(t, got, "|")
	})

	t.Run("head is truncated to msg0BlockHeadRunes runes", func(t *testing.T) {
		long := strings.Repeat("a", msg0BlockHeadRunes+50)
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": long},
					},
				},
			},
		}
		got := describeMsg0ContentBlocks(body)
		expectedHead := strings.Repeat("a", msg0BlockHeadRunes)
		require.Contains(t, got, "head="+expectedHead)
		require.NotContains(t, got, expectedHead+"a", "truncation must cap head at msg0BlockHeadRunes")
	})

	t.Run("non-text blocks log only their type", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "image", "source": map[string]any{}},
						map[string]any{"type": "tool_result", "tool_use_id": "x", "content": "y"},
						map[string]any{"type": "text", "text": "real"},
					},
				},
			},
		}
		got := describeMsg0ContentBlocks(body)
		require.Equal(t, "0:image|1:tool_result|2:text:len=4:head=real", got)
	})

	t.Run("non-object content element renders as non_object", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						"plain string element",
						map[string]any{"type": "text", "text": "x"},
					},
				},
			},
		}
		got := describeMsg0ContentBlocks(body)
		require.Equal(t, "0:non_object|1:text:len=1:head=x", got)
	})

	t.Run("untyped text element renders as untyped", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"text": "no type field"},
					},
				},
			},
		}
		got := describeMsg0ContentBlocks(body)
		require.Equal(t, "0:untyped", got)
	})

	t.Run("mirrors capture 025 /clear shape", func(t *testing.T) {
		body := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": "<system-reminder>a</system-reminder>"},
						map[string]any{"type": "text", "text": "<local-command-caveat>caveat</local-command-caveat>"},
						map[string]any{"type": "text", "text": "<command-name>/clear</command-name>"},
						map[string]any{"type": "text", "text": "<local-command-stdout></local-command-stdout>"},
						map[string]any{"type": "text", "text": "nihao"},
					},
				},
			},
		}
		got := describeMsg0ContentBlocks(body)
		require.Contains(t, got, "0:text:")
		require.Contains(t, got, "head=<system-reminder>a<")
		require.Contains(t, got, "head=<local-command-cave")
		require.Contains(t, got, "head=<command-name>/clear")
		require.Contains(t, got, "head=<local-command-stdo")
		require.Contains(t, got, "4:text:len=5:head=nihao")
	})
}

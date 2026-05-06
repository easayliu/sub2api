//go:build unit

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

func newTestValidator() *ClaudeCodeValidator {
	return NewClaudeCodeValidator()
}

// setValidCLIHeaders 写入一组真实 Claude CLI 抓包里观察到的合法 header，
// 给所有 messages 正向测试复用，避免重复罗列每个 header。
func setValidCLIHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "claude-cli/1.0.0")
	req.Header.Set("X-App", "cli")
	req.Header.Set("anthropic-beta", "claude-code-20250219")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
	req.Header.Set("X-Stainless-Lang", "js")
	req.Header.Set("X-Stainless-Package-Version", "0.81.0")
	req.Header.Set("X-Stainless-OS", "MacOS")
}

// validClaudeCodeBody 构造一个完整有效的 Claude Code 请求体
func validClaudeCodeBody() map[string]any {
	return map[string]any{
		"model": "claude-sonnet-4-20250514",
		"system": []any{
			map[string]any{
				"type": "text",
				"text": "You are Claude Code, Anthropic's official CLI for Claude.",
			},
		},
		"metadata": map[string]any{
			"user_id": "user_" + "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2" + "_account__session_" + "12345678-1234-1234-1234-123456789abc",
		},
	}
}

func TestValidate_ClaudeCLIUserAgent(t *testing.T) {
	v := newTestValidator()

	tests := []struct {
		name string
		ua   string
		want bool
	}{
		{"标准版本号", "claude-cli/1.0.0", true},
		{"多位版本号", "claude-cli/12.34.56", true},
		{"大写开头", "Claude-CLI/1.0.0", true},
		{"非 claude-cli", "curl/7.64.1", false},
		{"空 User-Agent", "", false},
		{"部分匹配", "not-claude-cli/1.0.0", false},
		{"缺少版本号", "claude-cli/", false},
		{"版本格式不对", "claude-cli/1.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, v.ValidateUserAgent(tt.ua), "UA: %q", tt.ua)
		})
	}
}

func TestValidate_NonMessagesPath_UAOnly(t *testing.T) {
	v := newTestValidator()

	// 非 messages 路径只检查 UA
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("User-Agent", "claude-cli/1.0.0")

	result := v.Validate(req, nil)
	require.True(t, result, "非 messages 路径只需 UA 匹配")
}

func TestValidate_NonMessagesPath_InvalidUA(t *testing.T) {
	v := newTestValidator()

	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("User-Agent", "curl/7.64.1")

	result := v.Validate(req, nil)
	require.False(t, result, "UA 不匹配时应返回 false")
}

func TestValidate_MessagesPath_FullValid(t *testing.T) {
	v := newTestValidator()

	req := httptest.NewRequest("POST", "/v1/messages", nil)
	setValidCLIHeaders(req)

	result := v.Validate(req, validClaudeCodeBody())
	require.True(t, result, "完整有效请求应通过")
}

// TestValidate_MessagesPath_StrictHeaderRejection 覆盖严格化的所有 header 拒绝路径。
func TestValidate_MessagesPath_StrictHeaderRejection(t *testing.T) {
	v := newTestValidator()

	tests := []struct {
		name        string
		mutate      func(req *http.Request)
		description string
	}{
		{
			name: "X-App 非 cli",
			mutate: func(req *http.Request) {
				req.Header.Set("X-App", "claude-code")
			},
			description: "X-App 必须是官方 CLI 的 'cli'",
		},
		{
			name: "anthropic-version 非 2023-06-01",
			mutate: func(req *http.Request) {
				req.Header.Set("anthropic-version", "2024-01-01")
			},
			description: "anthropic-version 必须严格匹配官方稳定版本",
		},
		{
			name: "anthropic-beta 不含 claude-code-20250219",
			mutate: func(req *http.Request) {
				req.Header.Set("anthropic-beta", "oauth-2025-04-20,interleaved-thinking-2025-05-14")
			},
			description: "anthropic-beta 必须包含 CLI 标识 token",
		},
		{
			name: "anthropic-beta 仅空白与逗号",
			mutate: func(req *http.Request) {
				req.Header.Set("anthropic-beta", " , , ")
			},
			description: "空 token 集应被拒",
		},
		{
			name: "anthropic-dangerous-direct-browser-access 缺失",
			mutate: func(req *http.Request) {
				req.Header.Del("anthropic-dangerous-direct-browser-access")
			},
			description: "缺少该 header 应被拒",
		},
		{
			name: "anthropic-dangerous-direct-browser-access 非 true",
			mutate: func(req *http.Request) {
				req.Header.Set("anthropic-dangerous-direct-browser-access", "false")
			},
			description: "该 header 必须为 true",
		},
		{
			name: "X-Stainless-Lang 非 js",
			mutate: func(req *http.Request) {
				req.Header.Set("X-Stainless-Lang", "python")
			},
			description: "CLI 走 Node SDK，X-Stainless-Lang 必须是 js",
		},
		{
			name: "X-Stainless-Lang 缺失",
			mutate: func(req *http.Request) {
				req.Header.Del("X-Stainless-Lang")
			},
			description: "缺少 X-Stainless-Lang 应被拒",
		},
		{
			name: "X-Stainless-Package-Version 缺失",
			mutate: func(req *http.Request) {
				req.Header.Del("X-Stainless-Package-Version")
			},
			description: "缺少 X-Stainless-Package-Version 应被拒",
		},
		{
			name: "X-Stainless-Package-Version 为空字符串",
			mutate: func(req *http.Request) {
				req.Header.Set("X-Stainless-Package-Version", "")
			},
			description: "X-Stainless-Package-Version 不能为空",
		},
		{
			name: "X-Stainless-OS 缺失",
			mutate: func(req *http.Request) {
				req.Header.Del("X-Stainless-OS")
			},
			description: "缺少 X-Stainless-OS 应被拒",
		},
		{
			name: "X-Stainless-OS 非已知值",
			mutate: func(req *http.Request) {
				req.Header.Set("X-Stainless-OS", "FreeBSD")
			},
			description: "X-Stainless-OS 必须是 CLI 已知 OS 之一",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/messages", nil)
			setValidCLIHeaders(req)
			tt.mutate(req)

			result := v.Validate(req, validClaudeCodeBody())
			require.False(t, result, tt.description)
		})
	}
}

// TestValidate_BillingHeaderSuffix 覆盖 cc_version 后三位 suffix 校验。
// 抓包数据来自 capture/2.1.123：last user text "你能做什么啊"、UA 2.1.123、
// 正确 suffix "d8c"。
func TestValidate_BillingHeaderSuffix(t *testing.T) {
	v := newTestValidator()

	const cliVersion = "2.1.123"
	const userText = "你能做什么啊"
	// 由 sha256("59cf53e54c78" + "么00" + "2.1.123")[:3] 算出，与抓包一致。
	const correctSuffix = "d8c"

	mkBody := func(billingHeader string, includeBilling bool) map[string]any {
		systems := []any{
			map[string]any{
				"type": "text",
				"text": "You are Claude Code, Anthropic's official CLI for Claude.",
			},
		}
		if includeBilling {
			systems = append([]any{
				map[string]any{"type": "text", "text": billingHeader},
			}, systems...)
		}
		return map[string]any{
			"model":  "claude-opus-4-7",
			"system": systems,
			"messages": []any{
				map[string]any{
					"role": "user",
					"content": []any{
						map[string]any{"type": "text", "text": userText},
					},
				},
			},
			"metadata": map[string]any{
				"user_id": `{"device_id":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2","account_uuid":"","session_id":"12345678-1234-1234-1234-123456789abc"}`,
			},
		}
	}

	mkReq := func(uaVersion string) *http.Request {
		req := httptest.NewRequest("POST", "/v1/messages", nil)
		setValidCLIHeaders(req)
		req.Header.Set("User-Agent", "claude-cli/"+uaVersion)
		return req
	}

	t.Run("正确 suffix 通过", func(t *testing.T) {
		req := mkReq(cliVersion)
		body := mkBody("x-anthropic-billing-header: cc_version="+cliVersion+"."+correctSuffix+"; cc_entrypoint=cli; cch=be2b5;", true)
		require.True(t, v.Validate(req, body))
	})

	t.Run("错误 suffix 拒绝", func(t *testing.T) {
		req := mkReq(cliVersion)
		body := mkBody("x-anthropic-billing-header: cc_version="+cliVersion+".000; cc_entrypoint=cli; cch=be2b5;", true)
		require.False(t, v.Validate(req, body))
	})

	t.Run("cc_version 与 UA 不一致拒绝", func(t *testing.T) {
		req := mkReq(cliVersion)
		body := mkBody("x-anthropic-billing-header: cc_version=2.0.0."+correctSuffix+"; cc_entrypoint=cli; cch=be2b5;", true)
		require.False(t, v.Validate(req, body))
	})

	t.Run("无 cc_version 字段拒绝", func(t *testing.T) {
		req := mkReq(cliVersion)
		body := mkBody("x-anthropic-billing-header: cc_entrypoint=cli; cch=be2b5;", true)
		require.False(t, v.Validate(req, body))
	})

	t.Run("UA 高于阈值但缺 billing header 拒绝", func(t *testing.T) {
		req := mkReq(cliVersion)
		body := mkBody("", false)
		require.False(t, v.Validate(req, body))
	})

	t.Run("UA 低于阈值缺 billing header 放行", func(t *testing.T) {
		req := mkReq("2.1.50") // < billingHeaderMinVersion 2.1.77
		body := mkBody("", false)
		require.True(t, v.Validate(req, body))
	})
}

// TestValidate_EnvBlock 覆盖 E1+E2：system 中 envBlockSentinel 段的 Platform/
// OS Version/Shell 三行必须存在且合法，且 Platform 必须与 X-Stainless-OS 对应。
func TestValidate_EnvBlock(t *testing.T) {
	v := newTestValidator()

	mkEnvText := func(platform, osVersion, shell string) string {
		// envBlockSentinel: "You have been invoked in the following environment:"
		// 模拟真实 CLI env 段的 markdown 结构。
		var sb strings.Builder
		sb.WriteString("# Environment\n")
		sb.WriteString(envBlockSentinel)
		sb.WriteString("\n")
		if platform != "" {
			sb.WriteString(" - Platform: " + platform + "\n")
		}
		if osVersion != "" {
			sb.WriteString(" - OS Version: " + osVersion + "\n")
		}
		if shell != "" {
			sb.WriteString(" - Shell: " + shell + "\n")
		}
		return sb.String()
	}

	mkBody := func(envText string, includeEnv bool) map[string]any {
		systems := []any{
			map[string]any{
				"type": "text",
				"text": "You are Claude Code, Anthropic's official CLI for Claude.",
			},
		}
		if includeEnv {
			systems = append(systems, map[string]any{
				"type": "text",
				"text": envText,
			})
		}
		return map[string]any{
			"model":  "claude-sonnet-4",
			"system": systems,
			"metadata": map[string]any{
				"user_id": `{"device_id":"a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2","account_uuid":"","session_id":"12345678-1234-1234-1234-123456789abc"}`,
			},
		}
	}

	mkReq := func(stainlessOS string) *http.Request {
		req := httptest.NewRequest("POST", "/v1/messages", nil)
		setValidCLIHeaders(req)
		if stainlessOS != "" {
			req.Header.Set("X-Stainless-OS", stainlessOS)
		}
		return req
	}

	t.Run("不含 env block 兼容通过", func(t *testing.T) {
		req := mkReq("MacOS")
		body := mkBody("", false)
		require.True(t, v.Validate(req, body))
	})

	t.Run("env block 完整且 Platform 与 X-Stainless-OS 一致", func(t *testing.T) {
		req := mkReq("MacOS")
		body := mkBody(mkEnvText("darwin", "Darwin 25.3.0", "zsh"), true)
		require.True(t, v.Validate(req, body))
	})

	t.Run("Linux 一致", func(t *testing.T) {
		req := mkReq("Linux")
		body := mkBody(mkEnvText("linux", "Linux 6.5.0", "bash"), true)
		require.True(t, v.Validate(req, body))
	})

	t.Run("Windows 一致", func(t *testing.T) {
		req := mkReq("Windows")
		body := mkBody(mkEnvText("win32", "Windows 11", "powershell"), true)
		require.True(t, v.Validate(req, body))
	})

	t.Run("env block 缺 Platform 行", func(t *testing.T) {
		req := mkReq("MacOS")
		body := mkBody(mkEnvText("", "Darwin 25.3.0", "zsh"), true)
		require.False(t, v.Validate(req, body))
	})

	t.Run("env block 缺 OS Version 行", func(t *testing.T) {
		req := mkReq("MacOS")
		body := mkBody(mkEnvText("darwin", "", "zsh"), true)
		require.False(t, v.Validate(req, body))
	})

	t.Run("env block 缺 Shell 行", func(t *testing.T) {
		req := mkReq("MacOS")
		body := mkBody(mkEnvText("darwin", "Darwin 25.3.0", ""), true)
		require.False(t, v.Validate(req, body))
	})

	t.Run("Platform 非已知值", func(t *testing.T) {
		req := mkReq("MacOS")
		body := mkBody(mkEnvText("freebsd", "FreeBSD 14.0", "csh"), true)
		require.False(t, v.Validate(req, body))
	})

	t.Run("Platform 与 X-Stainless-OS 不一致", func(t *testing.T) {
		req := mkReq("MacOS")
		body := mkBody(mkEnvText("linux", "Linux 6.5.0", "bash"), true)
		require.False(t, v.Validate(req, body))
	})
}

// TestValidate_StrictMetadataUserID_JSONFormat 覆盖 D 项对 JSON 分支的字段格式校验。
func TestValidate_StrictMetadataUserID_JSONFormat(t *testing.T) {
	v := newTestValidator()

	const validDeviceID = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	const validSessionID = "12345678-1234-1234-1234-123456789abc"

	tests := []struct {
		name   string
		userID string
		want   bool
	}{
		{
			name:   "合法 JSON metadata",
			userID: `{"device_id":"` + validDeviceID + `","account_uuid":"","session_id":"` + validSessionID + `"}`,
			want:   true,
		},
		{
			name:   "device_id 非 64-hex",
			userID: `{"device_id":"shortid","account_uuid":"","session_id":"` + validSessionID + `"}`,
			want:   false,
		},
		{
			name:   "device_id 含非 hex 字符",
			userID: `{"device_id":"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz","account_uuid":"","session_id":"` + validSessionID + `"}`,
			want:   false,
		},
		{
			name:   "session_id 长度不对",
			userID: `{"device_id":"` + validDeviceID + `","account_uuid":"","session_id":"too-short"}`,
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/messages", nil)
			setValidCLIHeaders(req)

			body := map[string]any{
				"model": "claude-sonnet-4",
				"system": []any{
					map[string]any{
						"type": "text",
						"text": "You are Claude Code, Anthropic's official CLI for Claude.",
					},
				},
				"metadata": map[string]any{
					"user_id": tt.userID,
				},
			}

			result := v.Validate(req, body)
			require.Equal(t, tt.want, result)
		})
	}
}

func TestValidate_MessagesPath_MissingHeaders(t *testing.T) {
	v := newTestValidator()
	body := validClaudeCodeBody()

	tests := []struct {
		name          string
		missingHeader string
	}{
		{"缺少 X-App", "X-App"},
		{"缺少 anthropic-beta", "anthropic-beta"},
		{"缺少 anthropic-version", "anthropic-version"},
		{"缺少 anthropic-dangerous-direct-browser-access", "anthropic-dangerous-direct-browser-access"},
		{"缺少 X-Stainless-Lang", "X-Stainless-Lang"},
		{"缺少 X-Stainless-Package-Version", "X-Stainless-Package-Version"},
		{"缺少 X-Stainless-OS", "X-Stainless-OS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/messages", nil)
			setValidCLIHeaders(req)
			req.Header.Del(tt.missingHeader)

			result := v.Validate(req, body)
			require.False(t, result, "缺少 %s 应返回 false", tt.missingHeader)
		})
	}
}

func TestValidate_MessagesPath_InvalidMetadataUserID(t *testing.T) {
	v := newTestValidator()

	tests := []struct {
		name     string
		metadata map[string]any
	}{
		{"缺少 metadata", nil},
		{"缺少 user_id", map[string]any{"other": "value"}},
		{"空 user_id", map[string]any{"user_id": ""}},
		{"格式错误", map[string]any{"user_id": "invalid-format"}},
		{"hex 长度不足", map[string]any{"user_id": "user_abc_account__session_uuid"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/messages", nil)
			setValidCLIHeaders(req)

			body := map[string]any{
				"model": "claude-sonnet-4",
				"system": []any{
					map[string]any{
						"type": "text",
						"text": "You are Claude Code, Anthropic's official CLI for Claude.",
					},
				},
			}
			if tt.metadata != nil {
				body["metadata"] = tt.metadata
			}

			result := v.Validate(req, body)
			require.False(t, result, "metadata.user_id: %v", tt.metadata)
		})
	}
}

func TestValidate_MessagesPath_InvalidSystemPrompt(t *testing.T) {
	v := newTestValidator()

	req := httptest.NewRequest("POST", "/v1/messages", nil)
	setValidCLIHeaders(req)

	body := map[string]any{
		"model": "claude-sonnet-4",
		"system": []any{
			map[string]any{
				"type": "text",
				"text": "Generate JSON data for testing database migrations.",
			},
		},
		"metadata": map[string]any{
			"user_id": "user_" + "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2" + "_account__session_12345678-1234-1234-1234-123456789abc",
		},
	}

	result := v.Validate(req, body)
	require.False(t, result, "无关系统提示词应返回 false")
}

func TestValidate_MaxTokensOneHaikuBypass(t *testing.T) {
	v := newTestValidator()

	req := httptest.NewRequest("POST", "/v1/messages", nil)
	req.Header.Set("User-Agent", "claude-cli/1.0.0")
	// 不设置 X-App 等头，通过 context 标记为 haiku 探测请求
	ctx := context.WithValue(req.Context(), ctxkey.IsMaxTokensOneHaikuRequest, true)
	req = req.WithContext(ctx)

	// 即使 body 不包含 system prompt，也应通过
	result := v.Validate(req, map[string]any{"model": "claude-3-haiku", "max_tokens": 1})
	require.True(t, result, "max_tokens=1+haiku 探测请求应绕过严格验证")
}

func TestSystemPromptSimilarity(t *testing.T) {
	v := newTestValidator()

	tests := []struct {
		name   string
		prompt string
		want   bool
	}{
		{"精确匹配", "You are Claude Code, Anthropic's official CLI for Claude.", true},
		{"带多余空格", "You  are  Claude  Code,  Anthropic's  official  CLI  for  Claude.", true},
		{"Agent SDK 模板", "You are a Claude agent, built on Anthropic's Claude Agent SDK.", true},
		{"文件搜索专家模板", "You are a file search specialist for Claude Code, Anthropic's official CLI for Claude.", true},
		{"对话摘要模板", "You are a helpful AI assistant tasked with summarizing conversations.", true},
		{"交互式 CLI 模板", "You are an interactive CLI tool that helps users", true},
		{"无关文本", "Write me a poem about cats", false},
		{"空文本", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]any{
				"model": "claude-sonnet-4",
				"system": []any{
					map[string]any{"type": "text", "text": tt.prompt},
				},
			}
			result := v.IncludesClaudeCodeSystemPrompt(body)
			require.Equal(t, tt.want, result, "提示词: %q", tt.prompt)
		})
	}
}

func TestDiceCoefficient(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want float64
		tol  float64
	}{
		{"相同字符串", "hello", "hello", 1.0, 0.001},
		{"完全不同", "abc", "xyz", 0.0, 0.001},
		{"空字符串", "", "hello", 0.0, 0.001},
		{"单字符", "a", "b", 0.0, 0.001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := diceCoefficient(tt.a, tt.b)
			require.InDelta(t, tt.want, result, tt.tol)
		})
	}
}

func TestIsClaudeCodeClient_Context(t *testing.T) {
	ctx := context.Background()

	// 默认应为 false
	require.False(t, IsClaudeCodeClient(ctx))

	// 设置为 true
	ctx = SetClaudeCodeClient(ctx, true)
	require.True(t, IsClaudeCodeClient(ctx))

	// 设置为 false
	ctx = SetClaudeCodeClient(ctx, false)
	require.False(t, IsClaudeCodeClient(ctx))
}

func TestValidate_NilBody_MessagesPath(t *testing.T) {
	v := newTestValidator()

	req := httptest.NewRequest("POST", "/v1/messages", nil)
	setValidCLIHeaders(req)

	result := v.Validate(req, nil)
	require.False(t, result, "nil body 的 messages 请求应返回 false")
}

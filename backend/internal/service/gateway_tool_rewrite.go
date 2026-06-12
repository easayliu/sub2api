package service

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"sort"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 工具名混淆（tool-name obfuscation）。
//
// 背景：当非 Claude Code 客户端（opencode / hermes-agent / 各类子 agent）经 OAuth
// 伪装路径转发时，其 tools[*].name 往往是任意业务名（delegate_task / skill_manage…）。
// 上游 Anthropic 的服务端校验器会据此把请求判为第三方应用，返回
// "Third-party apps now draw from your extra usage..." / "You're out of extra usage..."
// 之类的 400，即便订阅额度充足。
//
// 解法（对齐 Parrot cc_mimicry）：请求侧把这些工具名替换成 Claude-Code 风格的可读假名
// 转发，响应侧再按映射把假名还原成真名返回给客户端，全程对客户端透明。
// 仅在 OAuth 伪装路径（shouldMimicClaudeCode）生效；真实 CLI / APIKey 透传不改写。

// toolNameRewriteKey 是 gin.Context 上存 *ToolNameRewrite 的 key。
// 请求阶段写入，响应阶段读取，用于把假名逆向还原成真名。
const toolNameRewriteKey = "claude_tool_name_rewrite"

// staticToolNameRewrites 是“静态前缀映射”，与 Parrot TOOL_NAME_REWRITES 一致。
// 只有以这些前缀开头的工具会走前缀级重写（在动态映射未命中时兜底）。
var staticToolNameRewrites = map[string]string{
	"sessions_": "cc_sess_",
	"session_":  "cc_ses_",
}

// fakeToolNamePrefixes 是“动态映射”的前缀池，与 Parrot _FAKE_PREFIXES 一致。
var fakeToolNamePrefixes = []string{
	"analyze_", "compute_", "fetch_", "generate_", "lookup_", "modify_",
	"process_", "query_", "render_", "resolve_", "sync_", "update_",
	"validate_", "convert_", "extract_", "manage_", "monitor_", "parse_",
	"review_", "search_", "transform_", "handle_", "invoke_", "notify_",
}

// dynamicToolMapThreshold 控制动态映射的启用门槛：tools 数量 > 该值才混淆。
//
// 取 0（全覆盖）而非 Parrot/upstream 默认的 5。原因见 upstream PR #2163：
// 阈值 5 时，带 ≤5 个非白名单工具名的子 agent / 委托请求会裸传上游、触发同款
// 第三方 400（issue #1574 的反向边缘 case）。取 0 后只有空 tools 数组短路返回 nil，
// 任何非空工具集都混淆，彻底消除工具数量这个泄漏维度。
// 代价：响应侧每个 chunk 多 N 次字符串替换（N=工具数），已被 OAuth 伪装路径吸收。
const dynamicToolMapThreshold = 0

// ToolNameRewrite 是单次请求内的工具名混淆映射。
//   - Forward: real → fake，请求阶段在 body 上应用。
//   - ReverseOrdered: 按假名长度倒序的 (fake, real) 列表，响应阶段逐个 ReplaceAll
//     还原。倒序是为了防止短假名是长假名的子串时被先替换吃掉
//     （对齐 Parrot _restore_tool_names_in_chunk 的 sorted(..., reverse=True)）。
type ToolNameRewrite struct {
	Forward        map[string]string
	ReverseOrdered [][2]string
}

// buildDynamicToolMap 构造 tools 的动态假名映射。
//
// 与 Parrot _build_dynamic_tool_map 语义等价：
//   - tools 数量 ≤ dynamicToolMapThreshold（本仓为 0，即空数组）时返回 nil
//   - 同一组 tool_names 在同进程内映射稳定（保证上游 prompt cache 命中）
//
// Go 无法字节级复刻 Python hash，但“稳定性”和“前缀池打散”两个不变量都保留：
// 用 fnv64a(join(names, "\x00")) 作 seed 喂 math/rand。字节级不同不影响上游判定。
func buildDynamicToolMap(toolNames []string) map[string]string {
	if len(toolNames) <= dynamicToolMapThreshold {
		return nil
	}
	h := fnv.New64a()
	for i, n := range toolNames {
		if i > 0 {
			_, _ = h.Write([]byte{0})
		}
		_, _ = h.Write([]byte(n))
	}
	rng := rand.New(rand.NewSource(int64(h.Sum64())))

	available := make([]string, len(fakeToolNamePrefixes))
	copy(available, fakeToolNamePrefixes)
	rng.Shuffle(len(available), func(i, j int) { available[i], available[j] = available[j], available[i] })

	mapping := make(map[string]string, len(toolNames))
	for i, name := range toolNames {
		prefix := available[i%len(available)]
		headLen := 3
		if len(name) < 3 {
			headLen = len(name)
		}
		fake := fmt.Sprintf("%s%s%02d", prefix, name[:headLen], i)
		mapping[name] = fake
	}
	return mapping
}

// sanitizeToolName 把真名转成假名：动态映射优先，再走静态前缀映射，都不命中则原样返回。
func sanitizeToolName(name string, dynamic map[string]string) string {
	if dynamic != nil {
		if fake, ok := dynamic[name]; ok {
			return fake
		}
	}
	for prefix, replacement := range staticToolNameRewrites {
		if strings.HasPrefix(name, prefix) {
			return replacement + name[len(prefix):]
		}
	}
	return name
}

// shouldMimicToolName 指示某个 tool 是否可以重命名。
// server tool（type 非空且不是 "function" / "custom"）是 Anthropic 协议语义的一部分，
// 如 "web_search_20250305" / "computer_20250124"，误改会被上游拒绝，故跳过。
func shouldMimicToolName(toolType string) bool {
	if toolType == "" || toolType == "function" || toolType == "custom" {
		return true
	}
	return false
}

// buildToolNameRewriteFromBody 扫描 body 的 tools[*].name，构造 ToolNameRewrite。
// 若无可混淆的工具（tools 非数组 / 全是 server tool / 假名与真名相同）返回 nil。
// 只扫描不改 body；真正的 body 改写在 applyToolNameRewriteToBody。
func buildToolNameRewriteFromBody(body []byte) *ToolNameRewrite {
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return nil
	}

	mimicableNames := make([]string, 0)
	for _, t := range tools.Array() {
		if !shouldMimicToolName(t.Get("type").String()) {
			continue
		}
		name := t.Get("name").String()
		if name == "" {
			continue
		}
		mimicableNames = append(mimicableNames, name)
	}

	dynamic := buildDynamicToolMap(mimicableNames)

	rw := &ToolNameRewrite{Forward: make(map[string]string)}
	for _, name := range mimicableNames {
		fake := sanitizeToolName(name, dynamic)
		if fake == name {
			continue
		}
		rw.Forward[name] = fake
	}
	if len(rw.Forward) == 0 {
		return nil
	}

	rw.ReverseOrdered = make([][2]string, 0, len(rw.Forward))
	for real, fake := range rw.Forward {
		rw.ReverseOrdered = append(rw.ReverseOrdered, [2]string{fake, real})
	}
	sort.SliceStable(rw.ReverseOrdered, func(i, j int) bool {
		return len(rw.ReverseOrdered[i][0]) > len(rw.ReverseOrdered[j][0])
	})

	return rw
}

// applyToolNameRewriteToBody 把已构造的 ToolNameRewrite 应用到请求 body 上：
//   - 改写 $.tools[*].name（仅对 shouldMimicToolName 通过的 tool）
//   - 改写 $.tool_choice.name（仅当 $.tool_choice.type == "tool"）
//   - 改写 $.messages[*].content[*].name（仅当 type == "tool_use"）
//
// 不在此处注入 tools cache_control 断点——本仓的缓存断点由 upgradeCLICacheTTL /
// enforceCacheControlLimit 统一管理，避免突破 4 块上限。
func applyToolNameRewriteToBody(body []byte, rw *ToolNameRewrite) []byte {
	if rw == nil || len(rw.Forward) == 0 {
		return body
	}

	if tools := gjson.GetBytes(body, "tools"); tools.IsArray() {
		idx := -1
		tools.ForEach(func(_, t gjson.Result) bool {
			idx++
			if !shouldMimicToolName(t.Get("type").String()) {
				return true
			}
			name := t.Get("name").String()
			if name == "" {
				return true
			}
			fake, ok := rw.Forward[name]
			if !ok {
				return true
			}
			if next, err := sjson.SetBytes(body, fmt.Sprintf("tools.%d.name", idx), fake); err == nil {
				body = next
			}
			return true
		})
	}

	if tc := gjson.GetBytes(body, "tool_choice"); tc.Exists() && tc.Get("type").String() == "tool" {
		if fake, ok := rw.Forward[tc.Get("name").String()]; ok {
			if next, err := sjson.SetBytes(body, "tool_choice.name", fake); err == nil {
				body = next
			}
		}
	}

	// 同步改写历史消息中的 tool_use.name，确保与 tools[] 中的假名一致；
	// 否则上游会因 tool_use 引用了未声明的原始工具名而拒绝请求。
	if messages := gjson.GetBytes(body, "messages"); messages.IsArray() {
		messages.ForEach(func(msgKey, msg gjson.Result) bool {
			msgIdx := int(msgKey.Num)
			content := msg.Get("content")
			if !content.IsArray() {
				return true
			}
			content.ForEach(func(blkKey, blk gjson.Result) bool {
				blkIdx := int(blkKey.Num)
				if blk.Get("type").String() != "tool_use" {
					return true
				}
				name := blk.Get("name").String()
				if name == "" {
					return true
				}
				if fake, ok := rw.Forward[name]; ok {
					path := fmt.Sprintf("messages.%d.content.%d.name", msgIdx, blkIdx)
					if next, err := sjson.SetBytes(body, path, fake); err == nil {
						body = next
					}
				}
				return true
			})
			return true
		})
	}

	return body
}

// restoreToolNames 对响应侧字符串做逆向还原：假名 → 真名。
// 按 ReverseOrdered 的假名长度倒序逐个 ReplaceAll，防止子串冲突。
// rw 为 nil 或无映射时原样返回（真实 CLI / APIKey 透传路径零开销）。
func restoreToolNames(s string, rw *ToolNameRewrite) string {
	if rw == nil {
		return s
	}
	for _, pair := range rw.ReverseOrdered {
		fake, real := pair[0], pair[1]
		if fake == "" || fake == real {
			continue
		}
		if strings.Contains(s, fake) {
			s = strings.ReplaceAll(s, fake, real)
		}
	}
	return s
}

// toolNameRewriteFromContext 从 gin.Context 取出请求阶段保存的工具名映射。
// 找不到（c==nil / key 不存在 / 类型不对）时返回 nil；调用方必须能处理 nil。
func toolNameRewriteFromContext(c interface {
	Get(string) (any, bool)
}) *ToolNameRewrite {
	if c == nil {
		return nil
	}
	raw, ok := c.Get(toolNameRewriteKey)
	if !ok || raw == nil {
		return nil
	}
	rw, _ := raw.(*ToolNameRewrite)
	return rw
}

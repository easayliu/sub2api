package service

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// logRejected emits a warning-level structured log every time Validate rejects
// a request at Step 4. Step 1 UA failures are intentionally not logged here:
// random scanners / browsers / non-CLI tools constantly hit that path and the
// noise would drown out useful signal. Anything reaching Step 4 has already
// produced a CLI-shaped UA and is worth surfacing.
func logRejected(r *http.Request, step, reason string, extras ...any) {
	attrs := []any{
		"step", step,
		"reason", reason,
		"ua", r.Header.Get("User-Agent"),
		"path", r.URL.Path,
	}
	attrs = append(attrs, extras...)
	slog.Warn("claude_code_validator_reject", attrs...)
}

// ClaudeCodeValidator 验证请求是否来自 Claude Code 客户端
// 完全学习自 claude-relay-service 项目的验证逻辑
type ClaudeCodeValidator struct{}

var (
	// User-Agent 匹配: claude-cli/x.x.x (仅支持官方 CLI，大小写不敏感)
	claudeCodeUAPattern = regexp.MustCompile(`(?i)^claude-cli/\d+\.\d+\.\d+`)

	// 带捕获组的版本提取正则
	claudeCodeUAVersionPattern = regexp.MustCompile(`(?i)^claude-cli/(\d+\.\d+\.\d+)`)

	// System prompt 相似度阈值（默认 0.5，和 claude-relay-service 一致）
	systemPromptThreshold = 0.5

	// metadataDeviceIDPattern matches a 64-char hex device id used by the CLI.
	metadataDeviceIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

	// metadataSessionIDPattern matches a 36-char UUID-like session id.
	metadataSessionIDPattern = regexp.MustCompile(`^[a-fA-F0-9-]{36}$`)

	// ccVersionParseRe extracts cc_version=X.Y.Z.SSS from a billing header
	// text segment. The captures are (X.Y.Z, SSS).
	ccVersionParseRe = regexp.MustCompile(`cc_version=(\d+\.\d+\.\d+)\.([0-9a-f]+)`)

	// envPlatformExtractRe / envOSVersionExtractRe / envShellExtractRe extract
	// the value portion of the corresponding `- Field: value` line inside the
	// Claude Code system-prompt env block.
	envPlatformExtractRe  = regexp.MustCompile(`(?m)^[ \t]*-[ \t]+Platform:[ \t]+([^\r\n]+)`)
	envOSVersionExtractRe = regexp.MustCompile(`(?m)^[ \t]*-[ \t]+OS Version:[ \t]+([^\r\n]+)`)
	envShellExtractRe     = regexp.MustCompile(`(?m)^[ \t]*-[ \t]+Shell:[ \t]+([^\r\n]+)`)
)

// validClaudeCodePlatforms enumerates the Platform values that the official
// CLI writes into the env block. Anything else means the prompt was forged.
var validClaudeCodePlatforms = map[string]struct{}{
	"darwin": {},
	"linux":  {},
	"win32":  {},
}

// stainlessOSToPlatform maps X-Stainless-OS header values to the Platform
// string the CLI emits in the env block. Used to enforce that the wire-level
// OS fingerprint and the prompt-level env block stay in sync.
var stainlessOSToPlatform = map[string]string{
	"MacOS":   "darwin",
	"Linux":   "linux",
	"Windows": "win32",
}

// billingHeaderMinVersion is the first CLI release that emits the
// x-anthropic-billing-header system segment. Validation only enforces its
// presence/correctness on requests claiming a UA at or above this version.
const billingHeaderMinVersion = "2.1.77"

// expectedAnthropicVersion is the only Anthropic API version the official
// Claude CLI sends; non-matching values are treated as forged traffic.
const expectedAnthropicVersion = "2023-06-01"

// expectedXAppValue is the X-App header value emitted by the official CLI
// (see internal/pkg/claude/constants.go DefaultHeaders).
const expectedXAppValue = "cli"

// expectedStainlessLang is the X-Stainless-Lang value the Anthropic Node SDK
// (which the CLI ships) hard-codes for every request. Any deviation indicates
// a non-CLI client, so we treat it as a strict equality check.
const expectedStainlessLang = "js"

// hasRequiredCLIBetaToken reports whether the comma-separated anthropic-beta
// header carries the canonical CLI identifier token. Real Claude CLI traffic
// (>= 2.1.x) always emits claude.BetaClaudeCode on /v1/messages outside of
// haiku probes, so requiring it raises the cost of forging the header.
func hasRequiredCLIBetaToken(header string) bool {
	if header == "" {
		return false
	}
	for _, raw := range strings.Split(header, ",") {
		if strings.TrimSpace(raw) == claude.BetaClaudeCode {
			return true
		}
	}
	return false
}

// isStrictMetadataUserID enforces the field-level format expected from the
// official CLI on top of ParseMetadataUserID's structural parsing. The legacy
// format already enforces these via regex, so this primarily tightens the
// JSON branch where ParseMetadataUserID only checks for non-empty fields.
func isStrictMetadataUserID(parsed *ParsedUserID) bool {
	if parsed == nil {
		return false
	}
	if !metadataDeviceIDPattern.MatchString(parsed.DeviceID) {
		return false
	}
	if !metadataSessionIDPattern.MatchString(parsed.SessionID) {
		return false
	}
	return true
}

// Claude Code 官方 System Prompt 模板
// 从 claude-relay-service/src/utils/contents.js 提取
var claudeCodeSystemPrompts = []string{
	// claudeOtherSystemPrompt1 - Primary
	"You are Claude Code, Anthropic's official CLI for Claude.",

	// claudeOtherSystemPrompt3 - Agent SDK
	"You are a Claude agent, built on Anthropic's Claude Agent SDK.",

	// claudeOtherSystemPrompt4 - Compact Agent SDK
	"You are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK.",

	// exploreAgentSystemPrompt
	"You are a file search specialist for Claude Code, Anthropic's official CLI for Claude.",

	// claudeOtherSystemPromptCompact - Compact (用于对话摘要)
	"You are a helpful AI assistant tasked with summarizing conversations.",

	// claudeOtherSystemPrompt2 - Secondary (长提示词的关键部分)
	"You are an interactive CLI tool that helps users",
}

// NewClaudeCodeValidator 创建验证器实例
func NewClaudeCodeValidator() *ClaudeCodeValidator {
	return &ClaudeCodeValidator{}
}

// Validate 验证请求是否来自 Claude Code CLI
// 采用与 claude-relay-service 完全一致的验证策略：
//
//	Step 1: User-Agent 检查 (必需) - 必须是 claude-cli/x.x.x
//	Step 2: 对于非 messages 路径，只要 UA 匹配就通过
//	Step 3: 检查 max_tokens=1 + haiku 探测请求绕过（UA 已验证）
//	Step 3.5: count_tokens 路径绕过（UA 已验证）
//	Step 4: 对于 messages 路径，进行严格验证：
//	        - System prompt 相似度检查
//	        - X-App header 检查
//	        - anthropic-beta header 检查
//	        - anthropic-version header 检查
//	        - metadata.user_id 格式验证
func (v *ClaudeCodeValidator) Validate(r *http.Request, body map[string]any) bool {
	// Step 1: User-Agent 检查
	ua := r.Header.Get("User-Agent")
	if !claudeCodeUAPattern.MatchString(ua) {
		return false
	}

	// Step 2: 非 messages 路径，只要 UA 匹配就通过
	path := r.URL.Path
	if !strings.Contains(path, "messages") {
		return true
	}

	// Step 3: 检查 max_tokens=1 + haiku 探测请求绕过
	// 这类请求用于 Claude Code 验证 API 连通性，不携带 system prompt
	if isMaxTokensOneHaiku, ok := IsMaxTokensOneHaikuRequestFromContext(r.Context()); ok && isMaxTokensOneHaiku {
		return true // 绕过 system prompt 检查，UA 已在 Step 1 验证
	}

	// Step 3.5: bypass for count_tokens path.
	// count_tokens is a context-window estimator and does not carry the
	// full Claude Code system prompt or metadata, so Step 4 strict checks
	// would always reject it. UA match from Step 1 is sufficient evidence
	// that this is a real CLI probe.
	if strings.HasSuffix(path, "/count_tokens") {
		return true
	}

	// Step 4: messages 路径，进行严格验证

	// 4.1 检查 system prompt 相似度
	if !v.hasClaudeCodeSystemPrompt(body) {
		logRejected(r, "4.1_system_prompt", "no_matching_template")
		return false
	}

	// 4.2 严格校验必需的 headers，对齐真实 CLI 抓包指纹
	//   - X-App 必须等于官方 CLI 发出的 "cli"（大小写不敏感以兼容代理改写）
	//   - anthropic-version 必须等于官方稳定版本 "2023-06-01"
	//   - anthropic-beta 必须包含 CLI 标识 token claude-code-20250219
	//   - anthropic-dangerous-direct-browser-access 必须等于 "true"（CLI 硬编码）
	//   - X-Stainless-Lang 必须等于 "js"（Node SDK 固定值）
	//   - X-Stainless-Package-Version 必须存在（值随版本变化故只校验非空）
	//   - X-Stainless-OS 必须是 CLI 已知 OS 之一（MacOS/Linux/Windows）
	if !strings.EqualFold(r.Header.Get("X-App"), expectedXAppValue) {
		logRejected(r, "4.2_x_app", "mismatch", "x_app", r.Header.Get("X-App"))
		return false
	}

	if r.Header.Get("anthropic-version") != expectedAnthropicVersion {
		logRejected(r, "4.2_anthropic_version", "mismatch", "anthropic_version", r.Header.Get("anthropic-version"))
		return false
	}

	if !hasRequiredCLIBetaToken(r.Header.Get("anthropic-beta")) {
		logRejected(r, "4.2_anthropic_beta", "missing_claude_code_token", "anthropic_beta", r.Header.Get("anthropic-beta"))
		return false
	}

	if !strings.EqualFold(r.Header.Get("anthropic-dangerous-direct-browser-access"), "true") {
		logRejected(r, "4.2_dangerous_direct_browser_access", "not_true",
			"value", r.Header.Get("anthropic-dangerous-direct-browser-access"))
		return false
	}

	if !strings.EqualFold(r.Header.Get("X-Stainless-Lang"), expectedStainlessLang) {
		logRejected(r, "4.2_x_stainless_lang", "not_js", "x_stainless_lang", r.Header.Get("X-Stainless-Lang"))
		return false
	}

	if r.Header.Get("X-Stainless-Package-Version") == "" {
		logRejected(r, "4.2_x_stainless_package_version", "empty")
		return false
	}

	// X-Stainless-OS 必须是 CLI 已知的 OS 标识之一（MacOS/Linux/Windows），
	// 同时给 4.5 的 Platform 一致性校验提供锚点。
	if _, ok := stainlessOSToPlatform[r.Header.Get("X-Stainless-OS")]; !ok {
		logRejected(r, "4.2_x_stainless_os", "unknown", "x_stainless_os", r.Header.Get("X-Stainless-OS"))
		return false
	}

	// 4.3 验证 metadata.user_id（结构 + 字段级格式）
	if body == nil {
		logRejected(r, "4.3_metadata", "nil_body")
		return false
	}

	metadata, ok := body["metadata"].(map[string]any)
	if !ok {
		logRejected(r, "4.3_metadata", "missing_or_wrong_type")
		return false
	}

	userID, ok := metadata["user_id"].(string)
	if !ok || userID == "" {
		logRejected(r, "4.3_metadata_user_id", "missing_or_empty")
		return false
	}

	parsed := ParseMetadataUserID(userID)
	if !isStrictMetadataUserID(parsed) {
		// user_id contains device_id; log only the length to avoid leaking
		// fingerprint material into logs.
		logRejected(r, "4.3_metadata_user_id_format", "invalid_format", "user_id_len", len(userID))
		return false
	}

	// 4.4 校验 system 中 x-anthropic-billing-header 段的 cc_version 后三位。
	// CLI v2.1.77+ 通过 SHA256(salt + first_user_text[4,7,20] + version)[:3] 派生
	// 该后缀；伪造客户端要么完全省略 billing header，要么只能写出错误的 suffix。
	// 仅对发出该 header 的 CLI 版本（>= 2.1.77）强制存在性 + 正确性。
	if !v.validateBillingHeaderSuffix(r, body) {
		return false
	}

	// 4.5 env block 存在则严格校验。
	// system 中若出现 envBlockSentinel，则其 Platform/OS Version/Shell 三行
	// 必须都解析得到非空值，Platform 必须是 CLI 已知值，且与 X-Stainless-OS
	// 一致。compact / Agent SDK / explore agent 等模板可能不带 env block，
	// 因此仅在存在时强制校验，避免误杀。
	if !v.validateEnvBlock(r, body) {
		return false
	}

	return true
}

// validateEnvBlock 校验 system 中含 envBlockSentinel 段的环境信息合法性。
// 不存在 env block → 返回 true（兼容不带 env 的 CLI prompt 模板）。
func (v *ClaudeCodeValidator) validateEnvBlock(r *http.Request, body map[string]any) bool {
	envText, ok := findEnvBlockText(body)
	if !ok {
		return true
	}

	platform := extractEnvLineValue(envPlatformExtractRe, envText)
	osVersion := extractEnvLineValue(envOSVersionExtractRe, envText)
	shell := extractEnvLineValue(envShellExtractRe, envText)
	if platform == "" || osVersion == "" || shell == "" {
		logRejected(r, "4.5_env_block", "missing_field",
			"platform", platform, "os_version", osVersion, "shell", shell)
		return false
	}
	if _, ok := validClaudeCodePlatforms[platform]; !ok {
		logRejected(r, "4.5_env_block", "unknown_platform", "platform", platform)
		return false
	}

	// X-Stainless-OS 已在 4.2 校验为合法 key，可安全索引。
	xStainlessOS := r.Header.Get("X-Stainless-OS")
	expectedPlatform := stainlessOSToPlatform[xStainlessOS]
	if expectedPlatform != platform {
		logRejected(r, "4.5_env_block", "platform_os_mismatch",
			"platform", platform, "x_stainless_os", xStainlessOS, "expected_platform", expectedPlatform)
		return false
	}
	return true
}

// findEnvBlockText 返回 body.system 中第一个含 envBlockSentinel 的 text 段。
func findEnvBlockText(body map[string]any) (string, bool) {
	if body == nil {
		return "", false
	}
	systemEntries, ok := body["system"].([]any)
	if !ok {
		return "", false
	}
	for _, entry := range systemEntries {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		text, ok := entryMap["text"].(string)
		if !ok {
			continue
		}
		if strings.Contains(text, envBlockSentinel) {
			return text, true
		}
	}
	return "", false
}

// extractEnvLineValue 用提取型正则取出 `- Field: value` 行的 value（trim 空白）。
// 未匹配时返回空串。
func extractEnvLineValue(re *regexp.Regexp, text string) string {
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// validateBillingHeaderSuffix 校验请求 body system 中 x-anthropic-billing-header
// 段的 cc_version 后三位 suffix。返回 false 表示伪造或被篡改。
//
//   - UA version < 2.1.77：兼容旧版本，跳过校验
//   - billing header 段缺失（在 >= 2.1.77 下）：reject
//   - cc_version=X.Y.Z.SSS 解析失败、X.Y.Z 与 UA 不一致、SSS 不匹配重算结果：reject
func (v *ClaudeCodeValidator) validateBillingHeaderSuffix(r *http.Request, body map[string]any) bool {
	uaVersion := ExtractCLIVersion(r.Header.Get("User-Agent"))
	if uaVersion == "" {
		logRejected(r, "4.4_cc_version", "ua_version_unparseable")
		return false
	}
	// 老版本无 billing header，跳过该项校验
	if CompareVersions(uaVersion, billingHeaderMinVersion) < 0 {
		return true
	}

	billingText, ok := findBillingHeaderText(body)
	if !ok {
		logRejected(r, "4.4_cc_version", "billing_header_missing", "ua_version", uaVersion)
		return false
	}

	matches := ccVersionParseRe.FindStringSubmatch(billingText)
	if matches == nil {
		logRejected(r, "4.4_cc_version", "cc_version_unparseable",
			"ua_version", uaVersion, "billing_text", billingText)
		return false
	}
	parsedVersion, parsedSuffix := matches[1], matches[2]
	if parsedVersion != uaVersion {
		logRejected(r, "4.4_cc_version", "version_ua_mismatch",
			"ua_version", uaVersion, "parsed_version", parsedVersion)
		return false
	}

	firstUserText := extractFirstUserMessageTextFromMap(body)
	expected := computeBillingHeaderSuffixFromText(firstUserText, uaVersion)
	if parsedSuffix != expected {
		logRejected(r, "4.4_cc_version", "suffix_mismatch",
			"ua_version", uaVersion,
			"parsed_suffix", parsedSuffix,
			"expected_suffix", expected,
			"first_user_text_runes", utf8.RuneCountInString(firstUserText))
		return false
	}
	return true
}

// findBillingHeaderText 返回 body.system 中以 "x-anthropic-billing-header" 起首
// 的 text 段，若不存在返回 ok=false。
func findBillingHeaderText(body map[string]any) (string, bool) {
	if body == nil {
		return "", false
	}
	systemEntries, ok := body["system"].([]any)
	if !ok {
		return "", false
	}
	for _, entry := range systemEntries {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		text, ok := entryMap["text"].(string)
		if !ok {
			continue
		}
		if strings.HasPrefix(text, "x-anthropic-billing-header") {
			return text, true
		}
	}
	return "", false
}

// extractFirstUserMessageTextFromMap 是 extractFirstUserMessageText 的 map 版，
// 用于 validator 直接消费已解析后的 body。语义保持一致：取第一条 role==user
// 消息的最后一个 text 内容块。
func extractFirstUserMessageTextFromMap(body map[string]any) string {
	if body == nil {
		return ""
	}
	msgs, ok := body["messages"].([]any)
	if !ok {
		return ""
	}
	for _, m := range msgs {
		msgMap, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if role, _ := msgMap["role"].(string); role != "user" {
			continue
		}
		switch content := msgMap["content"].(type) {
		case string:
			return content
		case []any:
			var last string
			for _, item := range content {
				itemMap, ok := item.(map[string]any)
				if !ok {
					continue
				}
				if t, _ := itemMap["type"].(string); t != "text" {
					continue
				}
				if text, ok := itemMap["text"].(string); ok {
					last = text
				}
			}
			return last
		}
		return ""
	}
	return ""
}

// hasClaudeCodeSystemPrompt 检查请求是否包含 Claude Code 系统提示词
// 使用字符串相似度匹配（Dice coefficient）
func (v *ClaudeCodeValidator) hasClaudeCodeSystemPrompt(body map[string]any) bool {
	if body == nil {
		return false
	}

	// 检查 model 字段
	if _, ok := body["model"].(string); !ok {
		return false
	}

	// 获取 system 字段
	systemEntries, ok := body["system"].([]any)
	if !ok {
		return false
	}

	// 检查每个 system entry
	for _, entry := range systemEntries {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		text, ok := entryMap["text"].(string)
		if !ok || text == "" {
			continue
		}

		// 计算与所有模板的最佳相似度
		bestScore := v.bestSimilarityScore(text)
		if bestScore >= systemPromptThreshold {
			return true
		}
	}

	return false
}

// bestSimilarityScore 计算文本与所有 Claude Code 模板的最佳相似度
func (v *ClaudeCodeValidator) bestSimilarityScore(text string) float64 {
	normalizedText := normalizePrompt(text)
	bestScore := 0.0

	for _, template := range claudeCodeSystemPrompts {
		normalizedTemplate := normalizePrompt(template)
		score := diceCoefficient(normalizedText, normalizedTemplate)
		if score > bestScore {
			bestScore = score
		}
	}

	return bestScore
}

// normalizePrompt 标准化提示词文本（去除多余空白）
func normalizePrompt(text string) string {
	// 将所有空白字符替换为单个空格，并去除首尾空白
	return strings.Join(strings.Fields(text), " ")
}

// diceCoefficient 计算两个字符串的 Dice 系数（Sørensen–Dice coefficient）
// 这是 string-similarity 库使用的算法
// 公式: 2 * |intersection| / (|bigrams(a)| + |bigrams(b)|)
func diceCoefficient(a, b string) float64 {
	if a == b {
		return 1.0
	}

	if len(a) < 2 || len(b) < 2 {
		return 0.0
	}

	// 生成 bigrams
	bigramsA := getBigrams(a)
	bigramsB := getBigrams(b)

	if len(bigramsA) == 0 || len(bigramsB) == 0 {
		return 0.0
	}

	// 计算交集大小
	intersection := 0
	for bigram, countA := range bigramsA {
		if countB, exists := bigramsB[bigram]; exists {
			if countA < countB {
				intersection += countA
			} else {
				intersection += countB
			}
		}
	}

	// 计算总 bigram 数量
	totalA := 0
	for _, count := range bigramsA {
		totalA += count
	}
	totalB := 0
	for _, count := range bigramsB {
		totalB += count
	}

	return float64(2*intersection) / float64(totalA+totalB)
}

// getBigrams 获取字符串的所有 bigrams（相邻字符对）
func getBigrams(s string) map[string]int {
	bigrams := make(map[string]int)
	runes := []rune(strings.ToLower(s))

	for i := 0; i < len(runes)-1; i++ {
		bigram := string(runes[i : i+2])
		bigrams[bigram]++
	}

	return bigrams
}

// ValidateUserAgent 仅验证 User-Agent（用于不需要解析请求体的场景）
func (v *ClaudeCodeValidator) ValidateUserAgent(ua string) bool {
	return claudeCodeUAPattern.MatchString(ua)
}

// IncludesClaudeCodeSystemPrompt 检查请求体是否包含 Claude Code 系统提示词
// 只要存在匹配的系统提示词就返回 true（用于宽松检测）
func (v *ClaudeCodeValidator) IncludesClaudeCodeSystemPrompt(body map[string]any) bool {
	return v.hasClaudeCodeSystemPrompt(body)
}

// IsClaudeCodeClient 从 context 中获取 Claude Code 客户端标识
func IsClaudeCodeClient(ctx context.Context) bool {
	if v, ok := ctx.Value(ctxkey.IsClaudeCodeClient).(bool); ok {
		return v
	}
	return false
}

// SetClaudeCodeClient 将 Claude Code 客户端标识设置到 context 中
func SetClaudeCodeClient(ctx context.Context, isClaudeCode bool) context.Context {
	return context.WithValue(ctx, ctxkey.IsClaudeCodeClient, isClaudeCode)
}

// ExtractVersion 从 User-Agent 中提取 Claude Code 版本号
// 返回 "2.1.22" 形式的版本号，如果不匹配返回空字符串
func (v *ClaudeCodeValidator) ExtractVersion(ua string) string {
	return ExtractCLIVersion(ua)
}

// SetClaudeCodeVersion 将 Claude Code 版本号设置到 context 中
func SetClaudeCodeVersion(ctx context.Context, version string) context.Context {
	return context.WithValue(ctx, ctxkey.ClaudeCodeVersion, version)
}

// GetClaudeCodeVersion 从 context 中获取 Claude Code 版本号
func GetClaudeCodeVersion(ctx context.Context) string {
	if v, ok := ctx.Value(ctxkey.ClaudeCodeVersion).(string); ok {
		return v
	}
	return ""
}

// CompareVersions 比较两个 semver 版本号
// 返回: -1 (a < b), 0 (a == b), 1 (a > b)
func CompareVersions(a, b string) int {
	aParts := parseSemver(a)
	bParts := parseSemver(b)
	for i := 0; i < 3; i++ {
		if aParts[i] < bParts[i] {
			return -1
		}
		if aParts[i] > bParts[i] {
			return 1
		}
	}
	return 0
}

// parseSemver 解析 semver 版本号为 [major, minor, patch]
func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	result := [3]int{0, 0, 0}
	for i := 0; i < len(parts) && i < 3; i++ {
		if parsed, err := strconv.Atoi(parts[i]); err == nil {
			result[i] = parsed
		}
	}
	return result
}

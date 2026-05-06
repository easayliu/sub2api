package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// 预编译正则表达式（避免每次调用重新编译）
var (
	// 匹配 User-Agent 版本号: xxx/x.y.z
	userAgentVersionRegex = regexp.MustCompile(`/(\d+)\.(\d+)\.(\d+)`)

	// Matches a single env-block line like `  - Platform: darwin`. The capture
	// group keeps the prefix (leading whitespace + dash + key + colon + space)
	// so the replacement preserves the original indentation.
	//
	// Intra-line whitespace is restricted to `[ \t]` rather than `\s` because
	// in RE2 `\s` includes `\n`, which would let `\s+` span a blank value line
	// and consume the following line entirely (e.g. an empty `Platform:` line
	// would eat the subsequent `Shell:` line).
	envPlatformLineRegex  = regexp.MustCompile(`(?m)^([ \t]*-[ \t]+Platform:[ \t]+)[^\r\n]*`)
	envOSVersionLineRegex = regexp.MustCompile(`(?m)^([ \t]*-[ \t]+OS Version:[ \t]+)[^\r\n]*`)
	envShellLineRegex     = regexp.MustCompile(`(?m)^([ \t]*-[ \t]+Shell:[ \t]+)[^\r\n]*`)
	// envWorkingDirLineRegex matches the `Primary working directory:` line in
	// the env block. The first capture preserves the line prefix; the second
	// captures the raw value so we can normalize it to a Mac-style path.
	envWorkingDirLineRegex = regexp.MustCompile(`(?m)^([ \t]*-[ \t]+Primary working directory:[ \t]+)([^\r\n]*)`)
	// windowsDrivePathRegex detects `C:\foo` / `C:/foo` style absolute paths
	// (drive letter + colon + separator). Used by working-dir normalization to
	// convert Windows paths into a /Users/... shape consistent with
	// Platform: darwin.
	windowsDrivePathRegex = regexp.MustCompile(`^[A-Za-z]:[\\/](.*)$`)
)

// envBlockSentinel marks the Claude Code system-prompt env block. Rewriting is
// gated on this substring to avoid clobbering unrelated text that happens to
// contain `Platform:` or similar tokens. Also used by the inbound validator
// (claude_code_validator.go) to locate the env preamble.
const envBlockSentinel = "You have been invoked in the following environment:"

// Platform bucket names. Each (account, platform) pair holds an independent
// device_id and canonical profile so we never mix Mac/Win/Linux signals on a
// single fingerprint. Values match the X-Stainless-OS header verbatim so we
// can use that header directly as the cache-key suffix.
const (
	PlatformMacOS   = "MacOS"
	PlatformWindows = "Windows"
	PlatformLinux   = "Linux"
)

// platformProfiles holds the canonical Stainless / Prompt defaults for each
// platform bucket. Values are derived from real Claude Code CLI captures so
// non-Mac buckets pass natural-looking traffic upstream instead of a
// Mac-shaped rewrite.
var platformProfiles = map[string]Fingerprint{
	PlatformMacOS: {
		UserAgent:               "claude-cli/2.1.123 (external, cli)",
		StainlessLang:           "js",
		StainlessPackageVersion: "0.81.0",
		StainlessOS:             PlatformMacOS,
		StainlessArch:           "arm64",
		StainlessRuntime:        "node",
		StainlessRuntimeVersion: "v24.3.0",
		PromptPlatform:          "darwin",
		PromptOSVersion:         "Darwin 25.3.0",
		PromptShell:             "zsh",
	},
	PlatformWindows: {
		UserAgent:               "claude-cli/2.1.123 (external, cli)",
		StainlessLang:           "js",
		StainlessPackageVersion: "0.81.0",
		StainlessOS:             PlatformWindows,
		StainlessArch:           "x64",
		StainlessRuntime:        "node",
		StainlessRuntimeVersion: "v24.3.0",
		PromptPlatform:          "win32",
		PromptOSVersion:         "Windows 11 Enterprise 10.0.26200",
		PromptShell:             "PowerShell (use PowerShell syntax — e.g., $null not /dev/null, $env:VAR not $VAR, backtick for line continuation)",
	},
	PlatformLinux: {
		UserAgent:               "claude-cli/2.1.123 (external, cli)",
		StainlessLang:           "js",
		StainlessPackageVersion: "0.81.0",
		StainlessOS:             PlatformLinux,
		StainlessArch:           "x64",
		StainlessRuntime:        "node",
		StainlessRuntimeVersion: "v24.3.0",
		PromptPlatform:          "linux",
		PromptOSVersion:         "Linux 6.8.0-generic",
		PromptShell:             "bash",
	},
}

// platformProfile returns the canonical profile for a platform bucket. Unknown
// platform names fall back to the Mac profile so a stray X-Stainless-OS value
// does not create a third-class bucket.
func platformProfile(platform string) Fingerprint {
	if p, ok := platformProfiles[platform]; ok {
		return p
	}
	return platformProfiles[PlatformMacOS]
}

// defaultFingerprint preserves the previous package-level Mac fallback so test
// fixtures and any non-platform-aware callers keep working. New code should
// prefer platformProfile(platform) for explicit bucketing.
var defaultFingerprint = platformProfiles[PlatformMacOS]

// pinnedPlatform returns the platform bucket every account is locked to.
// All accounts deliberately share a single Mac identity so the upstream view
// of one account never jumps platforms — cross-platform drift on a single
// OAuth token is a stronger anti-detection signal than the occasional
// Windows/Linux marker that leaks through (working dir paths are normalized
// to Mac elsewhere). To re-enable per-client bucketing, replace callers of
// this with a header- or account-driven platform decision.
func pinnedPlatform() string {
	return PlatformMacOS
}

// Fingerprint represents account fingerprint data
type Fingerprint struct {
	ClientID                string
	UserAgent               string
	StainlessLang           string
	StainlessPackageVersion string
	StainlessOS             string
	StainlessArch           string
	StainlessRuntime        string
	StainlessRuntimeVersion string
	// PromptPlatform / PromptOSVersion / PromptShell are rewritten into the
	// Claude Code system prompt env block. They are always the locked Mac
	// profile values; never sourced from the inbound client.
	PromptPlatform  string
	PromptOSVersion string
	PromptShell     string
	UpdatedAt       int64 `json:",omitempty"` // Unix timestamp，用于判断是否需要续期TTL
}

// IdentityCache defines cache operations for identity service. All methods
// are scoped by `platform` so each (account, platform) bucket holds an
// independent device_id and masked-session id; this lets a single account
// pass through Mac/Windows/Linux clients without env-block rewriting.
type IdentityCache interface {
	GetFingerprint(ctx context.Context, accountID int64, platform string) (*Fingerprint, error)
	SetFingerprint(ctx context.Context, accountID int64, platform string, fp *Fingerprint) error
	// GetMaskedSessionID 获取固定的会话ID（用于会话ID伪装功能）
	// 返回的 sessionID 是一个 UUID 格式的字符串
	// 如果不存在或已过期（15分钟无请求），返回空字符串
	GetMaskedSessionID(ctx context.Context, accountID int64, platform string) (string, error)
	// SetMaskedSessionID 设置固定的会话ID，TTL 为 15 分钟
	// 每次调用都会刷新 TTL
	SetMaskedSessionID(ctx context.Context, accountID int64, platform, sessionID string) error
}

// IdentityService 管理OAuth账号的请求身份指纹
type IdentityService struct {
	cache IdentityCache
}

// NewIdentityService 创建新的IdentityService
func NewIdentityService(cache IdentityCache) *IdentityService {
	return &IdentityService{cache: cache}
}

// GetOrCreateFingerprint 获取或创建账号的固定指纹。
// 每个账号锁定到单一 platform bucket（当前为 Mac），保证 Anthropic 视角下
// 同一上游账号的 OS / Arch / env 永远一致；客户端实际平台不会污染身份。
func (s *IdentityService) GetOrCreateFingerprint(ctx context.Context, accountID int64, headers http.Header) (*Fingerprint, error) {
	platform := pinnedPlatform()

	// 尝试从缓存获取指纹
	cached, err := s.cache.GetFingerprint(ctx, accountID, platform)
	if err == nil && cached != nil {
		needWrite := false

		// Migrate cached fingerprints whose locked-profile fields drifted from
		// the bucket's canonical profile (covers both pre-bucketing legacy
		// keys and any field schema additions).
		if applyLockedProfile(cached, platform) {
			needWrite = true
		}

		// 检查客户端的user-agent是否是更新版本
		clientUA := headers.Get("User-Agent")
		if clientUA != "" && isNewerVersion(clientUA, cached.UserAgent) {
			// 版本升级：merge 语义 — 仅更新请求中实际携带的字段，保留缓存值
			// 避免缺失的头被硬编码默认值覆盖（如新 CLI 版本 + 旧 SDK 默认值的不一致）
			mergeHeadersIntoFingerprint(cached, headers, platform)
			needWrite = true
			logger.LegacyPrintf("service.identity", "Updated fingerprint for account %d/%s: %s (merge update)", accountID, platform, clientUA)
		} else if time.Since(time.Unix(cached.UpdatedAt, 0)) > 24*time.Hour {
			// 距上次写入超过24小时，续期TTL
			needWrite = true
		}

		if needWrite {
			cached.UpdatedAt = time.Now().Unix()
			if err := s.cache.SetFingerprint(ctx, accountID, platform, cached); err != nil {
				logger.LegacyPrintf("service.identity", "Warning: failed to refresh fingerprint for account %d/%s: %v", accountID, platform, err)
			}
		}
		return cached, nil
	}

	// 缓存不存在或解析失败，按当前平台创建新指纹
	fp := s.createFingerprintFromHeaders(headers, platform)

	// 生成随机ClientID
	fp.ClientID = generateClientID()
	fp.UpdatedAt = time.Now().Unix()

	// 保存到缓存（7天TTL，每24小时自动续期）
	if err := s.cache.SetFingerprint(ctx, accountID, platform, fp); err != nil {
		logger.LegacyPrintf("service.identity", "Warning: failed to cache fingerprint for account %d/%s: %v", accountID, platform, err)
	}

	logger.LegacyPrintf("service.identity", "Created new fingerprint for account %d/%s with client_id: %s", accountID, platform, fp.ClientID)
	return fp, nil
}

// createFingerprintFromHeaders 从请求头创建指纹，OS/Arch/Prompt* 锁定到入参
// platform 对应的 canonical profile（不再无条件锁 Mac）。
func (s *IdentityService) createFingerprintFromHeaders(headers http.Header, platform string) *Fingerprint {
	profile := platformProfile(platform)
	fp := &Fingerprint{}

	// 获取User-Agent
	if ua := headers.Get("User-Agent"); ua != "" {
		fp.UserAgent = ua
	} else {
		fp.UserAgent = profile.UserAgent
	}

	// Read x-stainless-* headers, falling back to platform defaults when absent.
	fp.StainlessLang = getHeaderOrDefault(headers, "X-Stainless-Lang", profile.StainlessLang)
	fp.StainlessPackageVersion = getHeaderOrDefault(headers, "X-Stainless-Package-Version", profile.StainlessPackageVersion)
	// OS/Arch are pinned to the platform bucket so they stay in sync with the
	// system-prompt env block (which we also lock to that platform), regardless
	// of any drift in the actual client headers.
	fp.StainlessOS = profile.StainlessOS
	fp.StainlessArch = profile.StainlessArch
	fp.StainlessRuntime = getHeaderOrDefault(headers, "X-Stainless-Runtime", profile.StainlessRuntime)
	fp.StainlessRuntimeVersion = getHeaderOrDefault(headers, "X-Stainless-Runtime-Version", profile.StainlessRuntimeVersion)

	// Prompt env fields are pinned to the platform bucket.
	fp.PromptPlatform = profile.PromptPlatform
	fp.PromptOSVersion = profile.PromptOSVersion
	fp.PromptShell = profile.PromptShell

	return fp
}

// mergeHeadersIntoFingerprint 将请求头中实际存在的字段合并到现有指纹中（用于版本升级场景）
// 关键语义：请求中有的字段 → 用新值覆盖；缺失的头 → 保留缓存中的已有值。
// OS/Arch/Prompt* 始终重置回 bucket 平台对应的 canonical profile，不随客户端漂移。
func mergeHeadersIntoFingerprint(fp *Fingerprint, headers http.Header, platform string) {
	profile := platformProfile(platform)
	// User-Agent：版本升级的触发条件，一定存在
	if ua := headers.Get("User-Agent"); ua != "" {
		fp.UserAgent = ua
	}
	// X-Stainless-* headers: update only when present in the request, otherwise
	// keep the cached value. Exception: OS/Arch are pinned to the bucket's
	// platform profile and never drift with the client.
	mergeHeader(headers, "X-Stainless-Lang", &fp.StainlessLang)
	mergeHeader(headers, "X-Stainless-Package-Version", &fp.StainlessPackageVersion)
	fp.StainlessOS = profile.StainlessOS
	fp.StainlessArch = profile.StainlessArch
	mergeHeader(headers, "X-Stainless-Runtime", &fp.StainlessRuntime)
	mergeHeader(headers, "X-Stainless-Runtime-Version", &fp.StainlessRuntimeVersion)

	// Prompt env fields are immutable per-bucket.
	fp.PromptPlatform = profile.PromptPlatform
	fp.PromptOSVersion = profile.PromptOSVersion
	fp.PromptShell = profile.PromptShell
}

// applyLockedProfile overwrites the fingerprint fields that are pinned to the
// bucket's platform profile (OS, Arch, Prompt env fields). Returns true if
// any field was changed, so the caller can persist the migration. Used both
// for legacy un-bucketed fingerprints (which were locked to Mac) and for any
// in-bucket drift caused by future schema additions.
func applyLockedProfile(fp *Fingerprint, platform string) bool {
	profile := platformProfile(platform)
	changed := false
	if fp.StainlessOS != profile.StainlessOS {
		fp.StainlessOS = profile.StainlessOS
		changed = true
	}
	if fp.StainlessArch != profile.StainlessArch {
		fp.StainlessArch = profile.StainlessArch
		changed = true
	}
	if fp.PromptPlatform != profile.PromptPlatform {
		fp.PromptPlatform = profile.PromptPlatform
		changed = true
	}
	if fp.PromptOSVersion != profile.PromptOSVersion {
		fp.PromptOSVersion = profile.PromptOSVersion
		changed = true
	}
	if fp.PromptShell != profile.PromptShell {
		fp.PromptShell = profile.PromptShell
		changed = true
	}
	return changed
}

// mergeHeader 如果请求头中存在该字段则更新目标值，否则保留原值
func mergeHeader(headers http.Header, key string, target *string) {
	if v := headers.Get(key); v != "" {
		*target = v
	}
}

// getHeaderOrDefault 获取header值，如果不存在则返回默认值
func getHeaderOrDefault(headers http.Header, key, defaultValue string) string {
	if v := headers.Get(key); v != "" {
		return v
	}
	return defaultValue
}

// ApplyFingerprint 将指纹应用到请求头（覆盖原有的x-stainless-*头）
// 使用 setHeaderRaw 保持原始大小写（如 X-Stainless-OS 而非 X-Stainless-Os）
func (s *IdentityService) ApplyFingerprint(req *http.Request, fp *Fingerprint) {
	if fp == nil {
		return
	}

	// 设置user-agent
	if fp.UserAgent != "" {
		setHeaderRaw(req.Header, "User-Agent", fp.UserAgent)
	}

	// 设置x-stainless-*头（保持与 claude.DefaultHeaders 一致的大小写）
	if fp.StainlessLang != "" {
		setHeaderRaw(req.Header, "X-Stainless-Lang", fp.StainlessLang)
	}
	if fp.StainlessPackageVersion != "" {
		setHeaderRaw(req.Header, "X-Stainless-Package-Version", fp.StainlessPackageVersion)
	}
	if fp.StainlessOS != "" {
		setHeaderRaw(req.Header, "X-Stainless-OS", fp.StainlessOS)
	}
	if fp.StainlessArch != "" {
		setHeaderRaw(req.Header, "X-Stainless-Arch", fp.StainlessArch)
	}
	if fp.StainlessRuntime != "" {
		setHeaderRaw(req.Header, "X-Stainless-Runtime", fp.StainlessRuntime)
	}
	if fp.StainlessRuntimeVersion != "" {
		setHeaderRaw(req.Header, "X-Stainless-Runtime-Version", fp.StainlessRuntimeVersion)
	}
}

// ApplyOSFingerprint overwrites only X-Stainless-OS and X-Stainless-Arch,
// leaving UA / Runtime / PackageVersion untouched. Used on the real-Claude-
// Code-CLI passthrough path where UA / cc_version must stay verbatim but
// OS / Arch still need to match the locked Mac profile so they stay in sync
// with the rewritten system-prompt env block.
func (s *IdentityService) ApplyOSFingerprint(req *http.Request, fp *Fingerprint) {
	if fp == nil {
		return
	}
	if fp.StainlessOS != "" {
		setHeaderRaw(req.Header, "X-Stainless-OS", fp.StainlessOS)
	}
	if fp.StainlessArch != "" {
		setHeaderRaw(req.Header, "X-Stainless-Arch", fp.StainlessArch)
	}
}

// RewriteEnvSection normalizes the Claude Code system-prompt env block
// (Platform / OS Version / Shell lines) so upstream sees a consistent
// client-OS picture regardless of the actual client platform. Only text
// blocks containing the env sentinel are touched. Returns the original
// body on any parse failure or when no change is needed.
func (s *IdentityService) RewriteEnvSection(body []byte, fp *Fingerprint) []byte {
	if len(body) == 0 || fp == nil {
		return body
	}
	if fp.PromptPlatform == "" && fp.PromptOSVersion == "" && fp.PromptShell == "" {
		return body
	}

	systemField := gjson.GetBytes(body, "system")
	if !systemField.Exists() {
		return body
	}

	// Claude Code sends system as an array of {type, text} blocks.
	// The env block typically lives in the last block.
	if !systemField.IsArray() {
		return body
	}

	result := body
	for i, block := range systemField.Array() {
		if block.Type != gjson.JSON {
			continue
		}
		textResult := block.Get("text")
		if !textResult.Exists() || textResult.Type != gjson.String {
			continue
		}
		orig := textResult.String()
		if !strings.Contains(orig, envBlockSentinel) {
			continue
		}
		rewritten := applyEnvLineRewrites(orig, fp)
		if rewritten == orig {
			continue
		}
		newBody, err := sjson.SetBytes(result, fmt.Sprintf("system.%d.text", i), rewritten)
		if err != nil {
			logger.LegacyPrintf("service.identity", "Warning: failed to rewrite system[%d].text env block: %v", i, err)
			continue
		}
		result = newBody
	}
	return result
}

// applyEnvLineRewrites replaces Platform / OS Version / Shell / working dir
// lines inside a single system-prompt text block. Each field is optional;
// empty values skip the corresponding substitution.
func applyEnvLineRewrites(text string, fp *Fingerprint) string {
	out := text
	if fp.PromptPlatform != "" {
		out = envPlatformLineRegex.ReplaceAllString(out, "${1}"+fp.PromptPlatform)
	}
	if fp.PromptOSVersion != "" {
		out = envOSVersionLineRegex.ReplaceAllString(out, "${1}"+fp.PromptOSVersion)
	}
	if fp.PromptShell != "" {
		out = envShellLineRegex.ReplaceAllString(out, "${1}"+fp.PromptShell)
	}
	// Normalize the Primary working directory value to a Mac-style /Users/...
	// path only when this fingerprint targets the Mac bucket. For Windows or
	// Linux buckets the env block is allowed to keep its native path style
	// because every other Stainless / Prompt field is also pinned to that
	// platform — they all match, so no rewriting is needed.
	if fp.PromptPlatform == "darwin" {
		out = envWorkingDirLineRegex.ReplaceAllStringFunc(out, func(line string) string {
			m := envWorkingDirLineRegex.FindStringSubmatch(line)
			if m == nil {
				return line
			}
			return m[1] + normalizeWorkingDirToMac(m[2])
		})
	}
	return out
}

// normalizeWorkingDirToMac converts a working-directory value into a Mac-style
// /Users/... path so it matches the rewritten Platform: darwin claim.
//   - Windows drive paths (`C:\Users\name\...` or `C:/...`) → `/Users/name/...`
//     when the first segment is `Users`, otherwise grafted under `/Users/user/`.
//   - Linux home (`/home/<user>/...`) → `/Users/<user>/...`.
//   - Already Mac-style (`/Users/...`) or other Unix paths pass through.
//   - UNC / backslash-only paths without a drive letter are converted to
//     forward slashes and grafted under `/Users/user/`.
func normalizeWorkingDirToMac(value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return value
	}

	if m := windowsDrivePathRegex.FindStringSubmatch(v); m != nil {
		rest := strings.ReplaceAll(m[1], `\`, `/`)
		rest = strings.TrimRight(rest, "/")
		if rest == "" {
			return "/Users/user"
		}
		if rest == "Users" || strings.HasPrefix(rest, "Users/") {
			return "/" + rest
		}
		return "/Users/user/" + rest
	}

	if strings.HasPrefix(v, "/home/") {
		return "/Users/" + strings.TrimPrefix(v, "/home/")
	}

	if strings.Contains(v, `\`) {
		cleaned := strings.ReplaceAll(strings.TrimLeft(v, `\`), `\`, `/`)
		cleaned = strings.TrimRight(cleaned, "/")
		if cleaned == "" {
			return "/Users/user"
		}
		if cleaned == "Users" || strings.HasPrefix(cleaned, "Users/") {
			return "/" + cleaned
		}
		return "/Users/user/" + cleaned
	}

	return v
}

// RewriteUserID 重写body中的metadata.user_id
// 支持旧拼接格式和新 JSON 格式的 user_id 解析，
// 根据 fingerprintUA 版本选择输出格式。
//
// 重要：此函数使用 json.RawMessage 保留其他字段的原始字节，
// 避免重新序列化导致 thinking 块等内容被修改。
func (s *IdentityService) RewriteUserID(body []byte, accountID int64, accountUUID, cachedClientID, fingerprintUA string) ([]byte, error) {
	if len(body) == 0 || accountUUID == "" || cachedClientID == "" {
		return body, nil
	}

	metadata := gjson.GetBytes(body, "metadata")
	if !metadata.Exists() || metadata.Type == gjson.Null {
		return body, nil
	}
	if !strings.HasPrefix(strings.TrimSpace(metadata.Raw), "{") {
		return body, nil
	}

	userIDResult := metadata.Get("user_id")
	if !userIDResult.Exists() || userIDResult.Type != gjson.String {
		return body, nil
	}
	userID := userIDResult.String()
	if userID == "" {
		return body, nil
	}

	// 解析 user_id（兼容旧拼接格式和新 JSON 格式）
	parsed := ParseMetadataUserID(userID)
	if parsed == nil {
		return body, nil
	}

	// 保留客户端原始 session_id：Claude CLI 主会话与子 agent 各自携带
	// 有语义的 session_id（主会话稳定、子 agent 每次刷新），Anthropic 侧
	// 据此区分真实会话结构。历史上这里做过 SHA256(accountID::session)
	// 派生以防跨账号关联，但代价是丢失原始会话结构；现在由 device_id 改写
	// （cachedClientID 覆盖原 device_id）承担跨账号隔离职责，session_id
	// 可以安全原样保留。如需进一步伪装，启用账号级 session_id_masking。
	newSessionHash := parsed.SessionID

	// Preserve original wire format (JSON vs legacy) based on what the
	// client actually sent, instead of relying on fingerprint UA version
	// extraction which may fail and silently downgrade JSON to legacy.
	newUserID := FormatMetadataUserIDPreserve(cachedClientID, accountUUID, newSessionHash, parsed.IsNewFormat)
	if newUserID == userID {
		return body, nil
	}

	newBody, err := sjson.SetBytes(body, "metadata.user_id", newUserID)
	if err != nil {
		return body, nil
	}
	return newBody, nil
}

// RewriteUserIDWithMasking 重写body中的metadata.user_id，支持会话ID伪装
// 如果账号启用了会话ID伪装（session_id_masking_enabled），
// 则在完成常规重写后，将 session 部分替换为固定的伪装ID（15分钟内保持不变）
//
// 重要：此函数使用 json.RawMessage 保留其他字段的原始字节，
// 避免重新序列化导致 thinking 块等内容被修改。
func (s *IdentityService) RewriteUserIDWithMasking(ctx context.Context, body []byte, account *Account, accountUUID, cachedClientID, fingerprintUA string, fp *Fingerprint) ([]byte, error) {
	// 先执行常规的 RewriteUserID 逻辑
	newBody, err := s.RewriteUserID(body, account.ID, accountUUID, cachedClientID, fingerprintUA)
	if err != nil {
		return newBody, err
	}

	// 检查是否启用会话ID伪装
	if !account.IsSessionIDMaskingEnabled() {
		return newBody, nil
	}

	metadata := gjson.GetBytes(newBody, "metadata")
	if !metadata.Exists() || metadata.Type == gjson.Null {
		return newBody, nil
	}
	if !strings.HasPrefix(strings.TrimSpace(metadata.Raw), "{") {
		return newBody, nil
	}

	userIDResult := metadata.Get("user_id")
	if !userIDResult.Exists() || userIDResult.Type != gjson.String {
		return newBody, nil
	}
	userID := userIDResult.String()
	if userID == "" {
		return newBody, nil
	}

	// 解析已重写的 user_id
	uidParsed := ParseMetadataUserID(userID)
	if uidParsed == nil {
		return newBody, nil
	}

	// Masked session id is keyed by the same (account, platform) bucket as the
	// fingerprint so a multi-platform account keeps a stable masked session per
	// platform instead of mixing them.
	platform := PlatformMacOS
	if fp != nil && fp.StainlessOS != "" {
		platform = fp.StainlessOS
	}

	// 获取或生成固定的伪装 session ID
	maskedSessionID, err := s.cache.GetMaskedSessionID(ctx, account.ID, platform)
	if err != nil {
		logger.LegacyPrintf("service.identity", "Warning: failed to get masked session ID for account %d/%s: %v", account.ID, platform, err)
		return newBody, nil
	}

	if maskedSessionID == "" {
		// 首次或已过期，生成新的伪装 session ID
		maskedSessionID = generateRandomUUID()
		logger.LegacyPrintf("service.identity", "Generated new masked session ID for account %d/%s: %s", account.ID, platform, maskedSessionID)
	}

	// 刷新 TTL（每次请求都刷新，保持 15 分钟有效期）
	if err := s.cache.SetMaskedSessionID(ctx, account.ID, platform, maskedSessionID); err != nil {
		logger.LegacyPrintf("service.identity", "Warning: failed to set masked session ID for account %d/%s: %v", account.ID, platform, err)
	}

	// Preserve original wire format (JSON vs legacy) based on parsed input.
	newUserID := FormatMetadataUserIDPreserve(uidParsed.DeviceID, uidParsed.AccountUUID, maskedSessionID, uidParsed.IsNewFormat)

	slog.Debug("session_id_masking_applied",
		"account_id", account.ID,
		"before", userID,
		"after", newUserID,
	)

	if newUserID == userID {
		return newBody, nil
	}

	maskedBody, setErr := sjson.SetBytes(newBody, "metadata.user_id", newUserID)
	if setErr != nil {
		return newBody, nil
	}
	return maskedBody, nil
}

// generateRandomUUID 生成随机 UUID v4 格式字符串
func generateRandomUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// fallback: 使用时间戳生成
		h := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
		b = h[:16]
	}

	// 设置 UUID v4 版本和变体位
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// generateClientID 生成64位十六进制客户端ID（32字节随机数）
func generateClientID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 极罕见的情况，使用时间戳+固定值作为fallback
		logger.LegacyPrintf("service.identity", "Warning: crypto/rand.Read failed: %v, using fallback", err)
		// 使用SHA256(当前纳秒时间)作为fallback
		h := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
		return hex.EncodeToString(h[:])
	}
	return hex.EncodeToString(b)
}

// parseUserAgentVersion 解析user-agent版本号
// 例如：claude-cli/2.1.2 -> (2, 1, 2)
func parseUserAgentVersion(ua string) (major, minor, patch int, ok bool) {
	// 匹配 xxx/x.y.z 格式
	matches := userAgentVersionRegex.FindStringSubmatch(ua)
	if len(matches) != 4 {
		return 0, 0, 0, false
	}
	major, _ = strconv.Atoi(matches[1])
	minor, _ = strconv.Atoi(matches[2])
	patch, _ = strconv.Atoi(matches[3])
	return major, minor, patch, true
}

// extractProduct 提取 User-Agent 中 "/" 前的产品名
// 例如：claude-cli/2.1.22 (external, cli) -> "claude-cli"
func extractProduct(ua string) string {
	if idx := strings.Index(ua, "/"); idx > 0 {
		return strings.ToLower(ua[:idx])
	}
	return ""
}

// isNewerVersion 比较版本号，判断newUA是否比cachedUA更新
// 要求产品名一致（防止浏览器 UA 如 Mozilla/5.0 误判为更新版本）
func isNewerVersion(newUA, cachedUA string) bool {
	// 校验产品名一致性
	newProduct := extractProduct(newUA)
	cachedProduct := extractProduct(cachedUA)
	if newProduct == "" || cachedProduct == "" || newProduct != cachedProduct {
		return false
	}

	newMajor, newMinor, newPatch, newOk := parseUserAgentVersion(newUA)
	cachedMajor, cachedMinor, cachedPatch, cachedOk := parseUserAgentVersion(cachedUA)

	if !newOk || !cachedOk {
		return false
	}

	// 比较版本号
	if newMajor > cachedMajor {
		return true
	}
	if newMajor < cachedMajor {
		return false
	}

	if newMinor > cachedMinor {
		return true
	}
	if newMinor < cachedMinor {
		return false
	}

	return newPatch > cachedPatch
}

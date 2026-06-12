package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestShouldMimicToolName(t *testing.T) {
	// 普通 custom/function tool 可混淆。
	assert.True(t, shouldMimicToolName(""))
	assert.True(t, shouldMimicToolName("function"))
	assert.True(t, shouldMimicToolName("custom"))
	// server tool 是协议语义的一部分，不能改名。
	assert.False(t, shouldMimicToolName("web_search_20250305"))
	assert.False(t, shouldMimicToolName("computer_20250124"))
	assert.False(t, shouldMimicToolName("bash_20250124"))
}

func TestBuildDynamicToolMap_ThresholdZero(t *testing.T) {
	// 阈值 0：空集合短路返回 nil。
	assert.Nil(t, buildDynamicToolMap(nil))
	assert.Nil(t, buildDynamicToolMap([]string{}))

	// 单个工具也混淆（本仓特性，区别于 upstream 阈值 5）。
	single := buildDynamicToolMap([]string{"bash"})
	require.Len(t, single, 1)
	assert.NotEqual(t, "bash", single["bash"])

	// 多个工具全部得到不同假名。
	names := []string{"delegate_task", "skill_manage", "skill_view"}
	m := buildDynamicToolMap(names)
	require.Len(t, m, 3)
	seen := map[string]bool{}
	for _, real := range names {
		fake := m[real]
		assert.NotEmpty(t, fake)
		assert.NotEqual(t, real, fake)
		assert.False(t, seen[fake], "fake name collision: %s", fake)
		seen[fake] = true
	}
}

func TestBuildDynamicToolMap_Stable(t *testing.T) {
	// 同一组工具名在同进程内映射稳定，保证上游 prompt cache 命中。
	names := []string{"delegate_task", "skill_manage", "session_search"}
	a := buildDynamicToolMap(names)
	b := buildDynamicToolMap(names)
	assert.Equal(t, a, b)
}

func TestBuildToolNameRewriteFromBody(t *testing.T) {
	t.Run("no tools array returns nil", func(t *testing.T) {
		assert.Nil(t, buildToolNameRewriteFromBody([]byte(`{"messages":[]}`)))
		assert.Nil(t, buildToolNameRewriteFromBody([]byte(`{"tools":{}}`)))
	})

	t.Run("server tools are skipped", func(t *testing.T) {
		body := []byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"}]}`)
		assert.Nil(t, buildToolNameRewriteFromBody(body))
	})

	t.Run("custom tools are obfuscated", func(t *testing.T) {
		body := []byte(`{"tools":[{"name":"delegate_task"},{"name":"skill_manage"}]}`)
		rw := buildToolNameRewriteFromBody(body)
		require.NotNil(t, rw)
		require.Len(t, rw.Forward, 2)
		assert.NotEqual(t, "delegate_task", rw.Forward["delegate_task"])
		assert.NotEqual(t, "skill_manage", rw.Forward["skill_manage"])
		// ReverseOrdered 覆盖每个假名，且按长度倒序。
		require.Len(t, rw.ReverseOrdered, 2)
		for i := 1; i < len(rw.ReverseOrdered); i++ {
			assert.GreaterOrEqual(t, len(rw.ReverseOrdered[i-1][0]), len(rw.ReverseOrdered[i][0]))
		}
	})
}

func TestApplyToolNameRewriteToBody(t *testing.T) {
	body := []byte(`{
		"tools":[{"name":"delegate_task"},{"type":"web_search_20250305","name":"web_search"}],
		"tool_choice":{"type":"tool","name":"delegate_task"},
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"delegate_task","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}
		]
	}`)
	rw := buildToolNameRewriteFromBody(body)
	require.NotNil(t, rw)
	fake := rw.Forward["delegate_task"]
	require.NotEmpty(t, fake)

	out := applyToolNameRewriteToBody(body, rw)

	// tools[0].name 改为假名；server tool 不动。
	assert.Equal(t, fake, gjson.GetBytes(out, "tools.0.name").String())
	assert.Equal(t, "web_search", gjson.GetBytes(out, "tools.1.name").String())
	// tool_choice.name 同步改写。
	assert.Equal(t, fake, gjson.GetBytes(out, "tool_choice.name").String())
	// 历史 tool_use.name 同步改写，避免引用未声明工具被拒。
	assert.Equal(t, fake, gjson.GetBytes(out, "messages.0.content.0.name").String())
	// 原始真名不应残留在 body 中。
	assert.NotContains(t, string(out), `"delegate_task"`)
}

func TestRestoreToolNames(t *testing.T) {
	t.Run("nil rewrite is passthrough", func(t *testing.T) {
		assert.Equal(t, "unchanged", restoreToolNames("unchanged", nil))
	})

	t.Run("substring-safe restore", func(t *testing.T) {
		// 短假名是长假名的子串：必须先还原长的，否则真名被破坏。
		rw := &ToolNameRewrite{
			Forward: map[string]string{"task": "fake", "task_long": "fakelong"},
			ReverseOrdered: [][2]string{
				{"fakelong", "task_long"},
				{"fake", "task"},
			},
		}
		got := restoreToolNames(`{"a":"fakelong","b":"fake"}`, rw)
		assert.Equal(t, `{"a":"task_long","b":"task"}`, got)
	})
}

func TestToolNameObfuscationRoundTrip(t *testing.T) {
	// 请求侧混淆 → 模拟上游用假名回流 → 响应侧还原回真名。
	body := []byte(`{"tools":[{"name":"delegate_task"},{"name":"skill_manage"}]}`)
	rw := buildToolNameRewriteFromBody(body)
	require.NotNil(t, rw)

	upstreamSees := applyToolNameRewriteToBody(body, rw)
	delegateFake := rw.Forward["delegate_task"]
	skillFake := rw.Forward["skill_manage"]

	// 上游响应里的 tool_use 用的是假名。
	upstreamResp := `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"` + delegateFake + `"}}

`
	restored := restoreToolNames(upstreamResp, rw)
	assert.Contains(t, restored, `"name":"delegate_task"`)
	assert.NotContains(t, restored, delegateFake)

	// upstreamSees 里确实是假名（请求侧已混淆）。
	assert.Equal(t, delegateFake, gjson.GetBytes(upstreamSees, "tools.0.name").String())
	assert.Equal(t, skillFake, gjson.GetBytes(upstreamSees, "tools.1.name").String())
}

// fakeCtx 实现 toolNameRewriteFromContext 需要的最小接口。
type fakeCtx struct {
	store map[string]any
}

func (f *fakeCtx) Get(key string) (any, bool) {
	v, ok := f.store[key]
	return v, ok
}

func TestToolNameRewriteFromContext(t *testing.T) {
	assert.Nil(t, toolNameRewriteFromContext(nil))

	empty := &fakeCtx{store: map[string]any{}}
	assert.Nil(t, toolNameRewriteFromContext(empty))

	rw := &ToolNameRewrite{Forward: map[string]string{"a": "b"}}
	withVal := &fakeCtx{store: map[string]any{toolNameRewriteKey: rw}}
	assert.Same(t, rw, toolNameRewriteFromContext(withVal))

	// 类型不对时安全返回 nil。
	wrongType := &fakeCtx{store: map[string]any{toolNameRewriteKey: "not-a-rewrite"}}
	assert.Nil(t, toolNameRewriteFromContext(wrongType))
}

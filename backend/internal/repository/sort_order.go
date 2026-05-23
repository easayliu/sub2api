package repository

import (
	entsql "entgo.io/ent/dialect/sql"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// orderField 生成单个排序字段的 ORDER BY 片段。
//
// 当 nullsLast 为 true 时追加 NULLS LAST，使无值（NULL）记录始终排在末尾。
// 否则 PostgreSQL 默认把 NULL 当作最大值，DESC 时会把空值排到最前面，
// 导致诸如"按最近使用时间倒序"时从未使用过的记录反而排在最前的问题。
//
// 适用于对可空字段（如 last_used_at、expires_at、used_at、starts_at）排序的场景；
// 对非空字段（nullsLast=false）退化为普通的 ASC/DESC。
func orderField(field string, desc, nullsLast bool) func(*entsql.Selector) {
	if !nullsLast {
		if desc {
			return dbent.Desc(field)
		}
		return dbent.Asc(field)
	}
	direction := " ASC NULLS LAST"
	if desc {
		direction = " DESC NULLS LAST"
	}
	return func(s *entsql.Selector) {
		s.OrderExprFunc(func(b *entsql.Builder) {
			b.Ident(s.C(field)).WriteString(direction)
		})
	}
}

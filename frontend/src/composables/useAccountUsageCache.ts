import { reactive } from 'vue'
import type { AccountUsageInfo } from '@/types'

interface AccountUsageCacheEntry {
  data: AccountUsageInfo | null
  // 拉取数据时的刷新签名（manualRefreshToken + openAI 刷新键）。
  // 签名一致说明缓存仍然新鲜，可直接复用；不一致则需要重新拉取。
  signature: string
}

// 模块级缓存：在 AccountUsageCell 因虚拟滚动 unmount / remount 之间保留 /usage 结果，
// 避免单元格滚出视口再滚回时重复请求并出现加载闪烁。按 account.id 缓存。
const cache = reactive(new Map<number, AccountUsageCacheEntry>())

export function useAccountUsageCache() {
  return {
    get(id: number): AccountUsageCacheEntry | undefined {
      return cache.get(id)
    },
    set(id: number, data: AccountUsageInfo | null, signature: string): void {
      cache.set(id, { data, signature })
    },
    invalidate(id: number): void {
      cache.delete(id)
    },
    clear(): void {
      cache.clear()
    }
  }
}

'use client'

import { useState } from 'react'
import {
  BanIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  SearchIcon,
  ShieldCheckIcon,
  Trash2Icon,
} from 'lucide-react'
import { useAuth } from '@/components/auth-provider'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useAnalyticsBans, useMe } from '@/hooks/use-api'
import { ApiError, deleteJson, postJson } from '@/lib/api'
import { formatNumber, timeAgo } from '@/lib/format'
import { cn } from '@/lib/utils'

const PAGE_SIZE = 20

function formatExpiry(expiresAt: number): string {
  if (!expiresAt) return '永久'
  const diffSec = expiresAt - Math.floor(Date.now() / 1000)
  if (diffSec <= 0) return '即将清理'
  const days = Math.floor(diffSec / 86400)
  const hrs = Math.floor((diffSec % 86400) / 3600)
  if (days > 0) return `${days}天${hrs}时后`
  if (hrs > 0) return `${hrs}小时后`
  return `${Math.max(1, Math.floor(diffSec / 60))}分钟后`
}

export function ShieldBansPanel() {
  const { token } = useAuth()
  const me = useMe()
  const isAdmin = me.data?.role === 'admin'

  const [q, setQ] = useState('')
  const [page, setPage] = useState(0)
  const [ipInput, setIpInput] = useState('')
  const [reasonInput, setReasonInput] = useState('')
  const [pendingKey, setPendingKey] = useState<string | null>(null)
  const [errMsg, setErrMsg] = useState('')

  const { data, isLoading, mutate } = useAnalyticsBans({
    q: q.trim() || undefined,
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
  })

  if (!data?.shield_enabled) return null

  async function handleManualBan(e: React.FormEvent) {
    e.preventDefault()
    const ip = ipInput.trim()
    if (!ip) return
    setPendingKey('_ban')
    setErrMsg('')
    try {
      await postJson('/api/analytics/bans', token, {
        ip,
        reason: reasonInput.trim() || 'manual',
        ttl_hours: data?.default_ttl_hours || 168,
      })
      setIpInput('')
      setReasonInput('')
      setPage(0)
      await mutate()
    } catch (err) {
      setErrMsg(err instanceof ApiError ? err.message : '封禁请求失败')
    } finally {
      setPendingKey(null)
    }
  }

  async function handleUnban(ip: string) {
    setPendingKey(ip)
    setErrMsg('')
    try {
      await deleteJson(`/api/analytics/bans?ip=${encodeURIComponent(ip)}`, token)
      await mutate()
    } catch (err) {
      setErrMsg(err instanceof ApiError ? err.message : '解封请求失败')
    } finally {
      setPendingKey(null)
    }
  }

  const total = data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <section className="rounded-2xl border bg-card">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3 sm:px-5 sm:py-3.5">
        <div className="space-y-0.5">
          <div className="flex flex-wrap items-center gap-2">
            <ShieldCheckIcon className="size-4 text-ok" />
            <h2 className="text-sm font-semibold">蜜罐防护与封禁 IP 列表</h2>
            <span className="rounded-full bg-muted px-2 py-0.5 font-mono text-[11px] text-muted-foreground tabular-nums">
              封禁中 {formatNumber(data.active_count)}
              {data.max_bans ? ` / ${formatNumber(data.max_bans)}` : ''}
            </span>
            <span className="rounded-full bg-muted px-2 py-0.5 font-mono text-[11px] text-muted-foreground">
              默认 {Math.round((data.default_ttl_hours || 168) / 24)} 天
            </span>
            {data.subnet_threshold ? (
              <span className="rounded-full bg-muted px-2 py-0.5 font-mono text-[11px] text-muted-foreground">
                同网段 ≥{data.subnet_threshold} 自动合并
              </span>
            ) : null}
          </div>
          <p className="text-xs text-muted-foreground">
            命中敏感蜜罐路径（返回诱饵）或漏洞扫描器 UA 后自动全站拦截（403）
          </p>
        </div>

        <div className="relative w-full sm:w-48">
          <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={q}
            onChange={(e) => {
              setQ(e.target.value)
              setPage(0)
            }}
            placeholder="搜索 IP / 路径 / 原因…"
            className="h-8 w-full pl-8 text-xs"
          />
        </div>
      </div>

      {isAdmin && (
        <form
          onSubmit={handleManualBan}
          className="flex flex-col gap-2 border-b bg-muted/20 px-4 py-3 sm:flex-row sm:items-center sm:px-5"
        >
          <Input
            value={ipInput}
            onChange={(e) => setIpInput(e.target.value)}
            placeholder="手动封禁 IP 或 CIDR（如 203.0.113.10 或 /24）"
            className="h-8 font-mono text-xs sm:w-64"
          />
          <Input
            value={reasonInput}
            onChange={(e) => setReasonInput(e.target.value)}
            placeholder="备注原因（可选）"
            className="h-8 text-xs sm:flex-1"
          />
          <Button
            type="submit"
            variant="outline"
            size="sm"
            disabled={!ipInput.trim() || pendingKey === '_ban'}
            className="shrink-0"
          >
            <BanIcon className="size-3.5 text-bad" />
            立即封禁
          </Button>
          {errMsg && (
            <span role="alert" className="text-xs text-bad">
              {errMsg}
            </span>
          )}
        </form>
      )}

      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs">
          <thead>
            <tr className="border-b bg-muted/35 text-muted-foreground">
              <th className="whitespace-nowrap px-4 py-2.5 font-medium">IP</th>
              <th className="whitespace-nowrap px-3.5 py-2.5 font-medium">触发原因</th>
              <th className="whitespace-nowrap px-3.5 py-2.5 font-medium">站点 / 路径 / UA</th>
              <th className="whitespace-nowrap px-3.5 py-2.5 text-right font-medium">命中次数</th>
              <th className="whitespace-nowrap px-3.5 py-2.5 text-right font-medium">最近触发</th>
              <th className="whitespace-nowrap px-3.5 py-2.5 text-right font-medium">剩余有效期</th>
              {isAdmin && <th className="whitespace-nowrap px-4 py-2.5 text-right font-medium">操作</th>}
            </tr>
          </thead>
          <tbody className="divide-y">
            {isLoading && data.rows.length === 0 ? (
              <tr>
                <td colSpan={isAdmin ? 7 : 6} className="px-4 py-8 text-center text-muted-foreground">
                  加载中…
                </td>
              </tr>
            ) : data.rows.length === 0 ? (
              <tr>
                <td colSpan={isAdmin ? 7 : 6} className="px-4 py-8 text-center text-muted-foreground">
                  暂无封禁中的 IP
                </td>
              </tr>
            ) : (
              data.rows.map((r) => (
                <tr key={r.ip} className="hover:bg-muted/30">
                  <td className="whitespace-nowrap px-4 py-2 font-mono font-medium">{r.ip}</td>
                  <td className="whitespace-nowrap px-3.5 py-2">
                    <span
                      className={cn(
                        'inline-flex items-center gap-1 rounded-full px-2 py-0.5 font-mono text-[11px]',
                        r.source === 'auto' ? 'bg-bad/12 text-bad' : 'bg-wait/15 text-wait',
                      )}
                    >
                      {r.source === 'auto' ? '自动' : '手动'} · {r.reason}
                    </span>
                  </td>
                  <td className="max-w-[14rem] px-3.5 py-2 sm:max-w-sm">
                    <div className="truncate font-mono text-foreground" title={r.sample_path}>
                      {r.site ? `[${r.site}] ` : ''}
                      {r.sample_path || '—'}
                    </div>
                    {r.sample_ua && (
                      <div className="truncate font-mono text-[11px] text-muted-foreground" title={r.sample_ua}>
                        {r.sample_ua}
                      </div>
                    )}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2 text-right font-mono tabular-nums">
                    {formatNumber(r.hits)}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2 text-right text-muted-foreground">
                    {timeAgo(new Date(r.last_seen_at * 1000).toISOString())}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2 text-right font-mono text-muted-foreground tabular-nums">
                    {formatExpiry(r.expires_at)}
                  </td>
                  {isAdmin && (
                    <td className="whitespace-nowrap px-4 py-2 text-right">
                      <Button
                        type="button"
                        variant="ghost"
                        size="sm"
                        disabled={pendingKey === r.ip}
                        onClick={() => handleUnban(r.ip)}
                        className="h-7 px-2 text-xs text-muted-foreground hover:text-bad"
                      >
                        <Trash2Icon className="size-3.5" />
                        解封
                      </Button>
                    </td>
                  )}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {pages > 1 && (
        <div className="flex items-center justify-between border-t px-4 py-3 text-xs text-muted-foreground sm:px-5">
          <span className="font-mono">
            共 {formatNumber(total)} 个 IP · 第 {page + 1} / {pages} 页
          </span>
          <div className="flex items-center gap-1">
            <Button
              variant="outline"
              size="icon-sm"
              disabled={page === 0}
              onClick={() => setPage((p) => p - 1)}
            >
              <ChevronLeftIcon />
            </Button>
            <Button
              variant="outline"
              size="icon-sm"
              disabled={page + 1 >= pages}
              onClick={() => setPage((p) => p + 1)}
            >
              <ChevronRightIcon />
            </Button>
          </div>
        </div>
      )}
    </section>
  )
}

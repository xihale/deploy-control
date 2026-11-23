'use client'

import { useMemo, useState } from 'react'
import Link from 'next/link'
import { useRouter, useSearchParams } from 'next/navigation'
import {
  ActivityIcon,
  AlertTriangleIcon,
  ArrowDownIcon,
  ArrowUpDownIcon,
  ArrowUpIcon,
  BotIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  CompassIcon,
  CpuIcon,
  DownloadIcon,
  ExternalLinkIcon,
  EyeIcon,
  FileTextIcon,
  GlobeIcon,
  HardDriveIcon,
  Link2Icon,
  MonitorSmartphoneIcon,
  RefreshCwIcon,
  RouteIcon,
  SearchIcon,
  ShieldAlertIcon,
  UsersIcon,
} from 'lucide-react'
import {
  BotBreakdownChart,
  DailyTrendChart,
  HourlyBarChart,
} from '@/components/analytics-charts'
import { useAuth } from '@/components/auth-provider'
import { ErrorState, PageSkeleton } from '@/components/states'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  useAnalyticsDimensions,
  useAnalyticsOverview,
  useAnalyticsSite,
  useSites,
} from '@/hooks/use-api'
import { buildQuery, downloadFile } from '@/lib/api'
import { formatBytes, formatMs, formatNumber, safeHttpUrl, siteUrlLabel } from '@/lib/format'
import { cn } from '@/lib/utils'
import type {
  AnalyticsDailyPoint,
  AnalyticsDimName,
  AnalyticsRangeName,
  AnalyticsSiteRow,
  AnalyticsSummary,
} from '@/lib/types'

const RANGES: { value: AnalyticsRangeName; label: string }[] = [
  { value: 'today', label: '今天' },
  { value: '7d', label: '近 7 天' },
  { value: '30d', label: '近 30 天' },
  { value: '90d', label: '近 90 天' },
]

const DIMENSIONS: {
  value: AnalyticsDimName
  label: string
  icon: React.ComponentType<{ className?: string }>
}[] = [
  { value: 'path', label: '热门页面', icon: FileTextIcon },
  { value: 'referrer', label: '访问来源', icon: Link2Icon },
  { value: 'browser', label: '浏览器', icon: CompassIcon },
  { value: 'os', label: '操作系统', icon: CpuIcon },
  { value: 'device', label: '设备类型', icon: MonitorSmartphoneIcon },
  { value: 'status', label: '状态码', icon: ActivityIcon },
  { value: 'error_path', label: '异常路径', icon: AlertTriangleIcon },
  { value: 'bot', label: '爬虫 / Bot', icon: BotIcon },
  { value: 'bot_path', label: 'Bot 抓取路径', icon: RouteIcon },
]

const DIM_PAGE_SIZE = 20

function DeltaBadge({ curr, prev }: { curr: number; prev: number }) {
  if (prev <= 0 && curr <= 0) {
    return <span className="font-mono text-xs text-muted-foreground">昨日 0</span>
  }
  if (prev <= 0) {
    return <span className="font-mono text-xs text-ok">昨日 0 · 新增</span>
  }
  const pct = ((curr - prev) / prev) * 100
  const sign = pct > 0 ? '+' : ''
  const cls = pct > 0 ? 'text-ok' : pct < 0 ? 'text-bad' : 'text-muted-foreground'
  return (
    <span className={cn('font-mono text-xs', cls)}>
      较昨日 {sign}
      {pct.toFixed(0)}% ({formatNumber(prev)})
    </span>
  )
}

function SummaryCards({ summary, isAll }: { summary: AnalyticsSummary; isAll: boolean }) {
  return (
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
      <div className="rounded-2xl border bg-card p-4 space-y-1.5">
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>{isAll ? '今日全站日活 (去重 DAU)' : '今日日活 (DAU)'}</span>
          <UsersIcon className="size-3.5 text-ok" />
        </div>
        <div className="font-mono text-2xl font-semibold tabular-nums">
          {formatNumber(summary.today_dau)}
        </div>
        <DeltaBadge curr={summary.today_dau} prev={summary.yesterday_dau} />
      </div>

      <div className="rounded-2xl border bg-card p-4 space-y-1.5">
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>区间独立访客 (UV)</span>
          <UsersIcon className="size-3.5 opacity-60" />
        </div>
        <div className="font-mono text-2xl font-semibold tabular-nums">
          {formatNumber(summary.range_uv)}
        </div>
        <div className="font-mono text-xs text-muted-foreground">
          7日 {formatNumber(summary.wau)} · 30日 {formatNumber(summary.mau)}
        </div>
      </div>

      <div className="rounded-2xl border bg-card p-4 space-y-1.5">
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>区间浏览量 (PV)</span>
          <EyeIcon className="size-3.5 text-run" />
        </div>
        <div className="font-mono text-2xl font-semibold tabular-nums">
          {formatNumber(summary.range_pv)}
        </div>
        <div className="font-mono text-xs text-muted-foreground">
          今日 {formatNumber(summary.today_pv)} · 人均 {summary.pv_per_uv.toFixed(2)} 页
        </div>
      </div>

      <div className="rounded-2xl border bg-card p-4 space-y-1.5">
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>总请求与流量</span>
          <HardDriveIcon className="size-3.5 text-wait" />
        </div>
        <div className="font-mono text-2xl font-semibold tabular-nums">
          {formatBytes(summary.range_bytes)}
        </div>
        <div className="font-mono text-xs text-muted-foreground">
          {formatNumber(summary.range_requests)} 请求 · 均耗 {formatMs(summary.avg_duration_ms)}
        </div>
      </div>

      <div className="rounded-2xl border bg-card p-4 space-y-1.5">
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>深度访问与异常/爬虫</span>
          <ShieldAlertIcon className="size-3.5 opacity-60" />
        </div>
        <div className="font-mono text-2xl font-semibold tabular-nums">
          {summary.engaged_rate.toFixed(1)}%
        </div>
        <div className="font-mono text-xs text-muted-foreground">
          <span className={summary.err_5xx > 0 ? 'text-bad' : undefined}>
            4xx/5xx {summary.err_4xx}/{summary.err_5xx}
          </span>{' '}
          · Bot {formatNumber(summary.bot_hits)}
        </div>
      </div>
    </div>
  )
}

function SortIcon({
  active,
  order,
}: {
  active: boolean
  order: 'asc' | 'desc'
}) {
  if (!active) return <ArrowUpDownIcon className="size-3 opacity-40" />
  return order === 'asc' ? (
    <ArrowUpIcon className="size-3 text-foreground" />
  ) : (
    <ArrowDownIcon className="size-3 text-foreground" />
  )
}

type SiteSortKey =
  | 'site'
  | 'today_dau'
  | 'wau'
  | 'mau'
  | 'range_uv'
  | 'today_pv'
  | 'range_pv'
  | 'pv_per_uv'
  | 'range_requests'
  | 'range_bytes'
  | 'avg_duration_ms'
  | 'err_4xx'
  | 'err_5xx'
  | 'bot_hits'

function SitesCompareTable({
  sites,
  onSelectSite,
  onExport,
}: {
  sites: AnalyticsSiteRow[]
  onSelectSite: (name: string) => void
  onExport: () => void
}) {
  const [q, setQ] = useState('')
  const [sortKey, setSortKey] = useState<SiteSortKey>('range_pv')
  const [order, setOrder] = useState<'asc' | 'desc'>('desc')

  function toggleSort(k: SiteSortKey) {
    if (sortKey === k) {
      setOrder((o) => (o === 'desc' ? 'asc' : 'desc'))
    } else {
      setSortKey(k)
      setOrder(k === 'site' ? 'asc' : 'desc')
    }
  }

  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase()
    const list = needle
      ? sites.filter(
          (s) =>
            s.site.toLowerCase().includes(needle) ||
            (s.url ?? '').toLowerCase().includes(needle) ||
            s.hosts.some((h) => h.toLowerCase().includes(needle)),
        )
      : [...sites]

    list.sort((a, b) => {
      const dir = order === 'asc' ? 1 : -1
      if (sortKey === 'site') return a.site.localeCompare(b.site) * dir
      return ((a[sortKey] as number) - (b[sortKey] as number)) * dir
    })
    return list
  }, [sites, q, sortKey, order])

  const cols: { key: SiteSortKey; label: string; align?: 'left' | 'right' }[] = [
    { key: 'site', label: '站点', align: 'left' },
    { key: 'today_dau', label: '今日 DAU' },
    { key: 'wau', label: '7日 WAU' },
    { key: 'mau', label: '30日 MAU' },
    { key: 'range_uv', label: '区间 UV' },
    { key: 'today_pv', label: '今日 PV' },
    { key: 'range_pv', label: '区间 PV' },
    { key: 'pv_per_uv', label: 'PV/UV' },
    { key: 'range_requests', label: '总请求' },
    { key: 'range_bytes', label: '流量' },
    { key: 'avg_duration_ms', label: '均耗' },
    { key: 'err_4xx', label: '4xx' },
    { key: 'err_5xx', label: '5xx' },
    { key: 'bot_hits', label: 'Bot' },
  ]

  return (
    <section className="rounded-2xl border bg-card">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-3.5">
        <div>
          <h2 className="text-sm font-semibold">各站点综合数据表</h2>
          <p className="text-xs text-muted-foreground">
            点击任意站点行可进入单站深度分析与维度明细
          </p>
        </div>
        <div className="flex items-center gap-2">
          <div className="relative">
            <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder="筛选站点或域名…"
              className="h-8 w-44 pl-8 text-xs"
            />
          </div>
          <Button variant="outline" size="sm" onClick={onExport}>
            <DownloadIcon className="size-3.5" />
            导出 CSV
          </Button>
        </div>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs">
          <thead>
            <tr className="border-b bg-muted/35 text-muted-foreground">
              {cols.map((c) => (
                <th
                  key={c.key}
                  className={cn(
                    'whitespace-nowrap px-3.5 py-2.5 font-medium',
                    c.align !== 'left' && 'text-right',
                  )}
                >
                  <button
                    type="button"
                    onClick={() => toggleSort(c.key)}
                    className={cn(
                      'inline-flex items-center gap-1 hover:text-foreground',
                      c.align !== 'left' && 'flex-row-reverse',
                    )}
                  >
                    <span>{c.label}</span>
                    <SortIcon active={sortKey === c.key} order={order} />
                  </button>
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y">
            {filtered.length === 0 ? (
              <tr>
                <td colSpan={cols.length} className="px-4 py-8 text-center text-muted-foreground">
                  没有匹配的站点数据
                </td>
              </tr>
            ) : (
              filtered.map((s) => (
                <tr
                  key={s.site}
                  onClick={() => onSelectSite(s.site)}
                  className="cursor-pointer transition-colors hover:bg-muted/40"
                >
                  <td className="whitespace-nowrap px-3.5 py-2.5">
                    <div className="font-medium text-foreground">{s.site}</div>
                    <div className="font-mono text-[11px] text-muted-foreground">
                      {s.hosts[0] ?? (s.url ? siteUrlLabel(s.url) : '未绑定域名')}
                    </div>
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2.5 text-right font-mono font-medium tabular-nums">
                    {formatNumber(s.today_dau)}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2.5 text-right font-mono tabular-nums text-muted-foreground">
                    {formatNumber(s.wau)}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2.5 text-right font-mono tabular-nums text-muted-foreground">
                    {formatNumber(s.mau)}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2.5 text-right font-mono tabular-nums">
                    {formatNumber(s.range_uv)}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2.5 text-right font-mono tabular-nums text-muted-foreground">
                    {formatNumber(s.today_pv)}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2.5 text-right font-mono font-medium tabular-nums">
                    {formatNumber(s.range_pv)}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2.5 text-right font-mono tabular-nums text-muted-foreground">
                    {s.pv_per_uv.toFixed(2)}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2.5 text-right font-mono tabular-nums text-muted-foreground">
                    {formatNumber(s.range_requests)}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2.5 text-right font-mono tabular-nums">
                    {formatBytes(s.range_bytes)}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2.5 text-right font-mono tabular-nums text-muted-foreground">
                    {formatMs(s.avg_duration_ms)}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2.5 text-right font-mono tabular-nums text-muted-foreground">
                    {formatNumber(s.err_4xx)}
                  </td>
                  <td
                    className={cn(
                      'whitespace-nowrap px-3.5 py-2.5 text-right font-mono tabular-nums',
                      s.err_5xx > 0 ? 'font-medium text-bad' : 'text-muted-foreground',
                    )}
                  >
                    {formatNumber(s.err_5xx)}
                  </td>
                  <td className="whitespace-nowrap px-3.5 py-2.5 text-right font-mono tabular-nums text-muted-foreground">
                    {formatNumber(s.bot_hits)}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}

type DailySortKey =
  | 'day'
  | 'dau'
  | 'pv'
  | 'pv_per_uv'
  | 'requests'
  | 'bytes'
  | 'avg_duration_ms'
  | 'err_4xx'
  | 'err_5xx'
  | 'bot_hits'

function DailyBreakdownTable({
  rows,
  title,
  onExport,
}: {
  rows: AnalyticsDailyPoint[]
  title: string
  onExport: () => void
}) {
  const [sortKey, setSortKey] = useState<DailySortKey>('day')
  const [order, setOrder] = useState<'asc' | 'desc'>('desc')

  function toggleSort(k: DailySortKey) {
    if (sortKey === k) {
      setOrder((o) => (o === 'desc' ? 'asc' : 'desc'))
    } else {
      setSortKey(k)
      setOrder('desc')
    }
  }

  const sorted = useMemo(() => {
    const list = [...rows]
    list.sort((a, b) => {
      const dir = order === 'asc' ? 1 : -1
      if (sortKey === 'day') return a.day.localeCompare(b.day) * dir
      return ((a[sortKey] as number) - (b[sortKey] as number)) * dir
    })
    return list
  }, [rows, sortKey, order])

  const cols: { key: DailySortKey; label: string; align?: 'left' | 'right' }[] = [
    { key: 'day', label: '日期', align: 'left' },
    { key: 'dau', label: 'DAU' },
    { key: 'pv', label: 'PV' },
    { key: 'pv_per_uv', label: 'PV/UV' },
    { key: 'requests', label: '总请求' },
    { key: 'bytes', label: '流量' },
    { key: 'avg_duration_ms', label: '均耗' },
    { key: 'err_4xx', label: '4xx' },
    { key: 'err_5xx', label: '5xx' },
    { key: 'bot_hits', label: 'Bot' },
  ]

  return (
    <section className="rounded-2xl border bg-card">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-3.5">
        <h2 className="text-sm font-semibold">{title}</h2>
        <Button variant="outline" size="sm" onClick={onExport}>
          <DownloadIcon className="size-3.5" />
          导出 CSV
        </Button>
      </div>
      <div className="max-h-96 overflow-auto">
        <table className="w-full text-left text-xs">
          <thead className="sticky top-0 bg-card shadow-[inset_0_-1px_0_var(--border)]">
            <tr className="bg-muted/35 text-muted-foreground">
              {cols.map((c) => (
                <th
                  key={c.key}
                  className={cn(
                    'whitespace-nowrap px-3.5 py-2.5 font-medium',
                    c.align !== 'left' && 'text-right',
                  )}
                >
                  <button
                    type="button"
                    onClick={() => toggleSort(c.key)}
                    className={cn(
                      'inline-flex items-center gap-1 hover:text-foreground',
                      c.align !== 'left' && 'flex-row-reverse',
                    )}
                  >
                    <span>{c.label}</span>
                    <SortIcon active={sortKey === c.key} order={order} />
                  </button>
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y">
            {sorted.map((r) => (
              <tr key={r.day} className="hover:bg-muted/30">
                <td className="whitespace-nowrap px-3.5 py-2 font-mono font-medium">
                  {r.day}
                </td>
                <td className="whitespace-nowrap px-3.5 py-2 text-right font-mono tabular-nums">
                  {formatNumber(r.dau)}
                </td>
                <td className="whitespace-nowrap px-3.5 py-2 text-right font-mono tabular-nums">
                  {formatNumber(r.pv)}
                </td>
                <td className="whitespace-nowrap px-3.5 py-2 text-right font-mono tabular-nums text-muted-foreground">
                  {r.pv_per_uv.toFixed(2)}
                </td>
                <td className="whitespace-nowrap px-3.5 py-2 text-right font-mono tabular-nums text-muted-foreground">
                  {formatNumber(r.requests)}
                </td>
                <td className="whitespace-nowrap px-3.5 py-2 text-right font-mono tabular-nums">
                  {formatBytes(r.bytes)}
                </td>
                <td className="whitespace-nowrap px-3.5 py-2 text-right font-mono tabular-nums text-muted-foreground">
                  {formatMs(r.avg_duration_ms)}
                </td>
                <td className="whitespace-nowrap px-3.5 py-2 text-right font-mono tabular-nums text-muted-foreground">
                  {formatNumber(r.err_4xx)}
                </td>
                <td
                  className={cn(
                    'whitespace-nowrap px-3.5 py-2 text-right font-mono tabular-nums',
                    r.err_5xx > 0 ? 'font-medium text-bad' : 'text-muted-foreground',
                  )}
                >
                  {formatNumber(r.err_5xx)}
                </td>
                <td className="whitespace-nowrap px-3.5 py-2 text-right font-mono tabular-nums text-muted-foreground">
                  {formatNumber(r.bot_hits)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function DimensionBreakdownTable({
  site,
  range,
  from,
  to,
}: {
  site: string
  range: AnalyticsRangeName
  from: string
  to: string
}) {
  const { token } = useAuth()
  const [dim, setDim] = useState<AnalyticsDimName>('path')
  const [search, setSearch] = useState('')
  const [sort, setSort] = useState<string>('pv')
  const [order, setOrder] = useState<'asc' | 'desc'>('desc')
  const [page, setPage] = useState(0)

  const queryParams = {
    site,
    dim,
    range: from || to ? undefined : range,
    from: from || undefined,
    to: to || undefined,
    q: search.trim() || undefined,
    sort,
    order,
    limit: DIM_PAGE_SIZE,
    offset: page * DIM_PAGE_SIZE,
  }
  const { data, isLoading, error } = useAnalyticsDimensions(queryParams)

  function switchDim(next: AnalyticsDimName) {
    setDim(next)
    setPage(0)
    setSearch('')
    setSort(
      next === 'status' || next === 'error_path' || next === 'bot' || next === 'bot_path'
        ? 'requests'
        : 'pv',
    )
    setOrder('desc')
  }

  function toggleSort(col: string) {
    setPage(0)
    if (sort === col) {
      setOrder((o) => (o === 'desc' ? 'asc' : 'desc'))
    } else {
      setSort(col)
      setOrder(col === 'key' ? 'asc' : 'desc')
    }
  }

  async function handleExport() {
    const qs = buildQuery({
      type: 'dimension',
      site,
      dim,
      range: from || to ? undefined : range,
      from: from || undefined,
      to: to || undefined,
      q: search.trim() || undefined,
      sort,
      order,
    })
    await downloadFile(
      `/api/analytics/export${qs}`,
      token,
      `analytics-${site}-${dim}.csv`,
    )
  }

  const total = data?.total ?? 0
  const pages = Math.max(1, Math.ceil(total / DIM_PAGE_SIZE))

  return (
    <section className="rounded-2xl border bg-card">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-3.5">
        <div className="flex flex-wrap gap-1.5">
          {DIMENSIONS.map((d) => {
            const Icon = d.icon
            return (
              <button
                key={d.value}
                type="button"
                onClick={() => switchDim(d.value)}
                className={cn(
                  'inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs transition-colors',
                  dim === d.value
                    ? 'border-foreground bg-foreground text-background'
                    : 'text-muted-foreground hover:border-foreground/30 hover:text-foreground',
                )}
              >
                <Icon className="size-3.5" />
                {d.label}
              </button>
            )
          })}
        </div>

        <div className="flex items-center gap-2">
          <div className="relative">
            <SearchIcon className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={search}
              onChange={(e) => {
                setSearch(e.target.value)
                setPage(0)
              }}
              placeholder="搜索维度项…"
              className="h-8 w-44 pl-8 text-xs"
            />
          </div>
          <Button variant="outline" size="sm" onClick={handleExport}>
            <DownloadIcon className="size-3.5" />
            导出 CSV
          </Button>
        </div>
      </div>

      {error ? (
        <div className="p-6 text-center text-sm text-bad">加载维度表失败</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead>
              <tr className="border-b bg-muted/35 text-muted-foreground">
                <th className="px-4 py-2.5 font-medium">
                  <button
                    type="button"
                    onClick={() => toggleSort('key')}
                    className="inline-flex items-center gap-1 hover:text-foreground"
                  >
                    <span>维度值</span>
                    <SortIcon active={sort === 'key'} order={order} />
                  </button>
                </th>
                <th className="px-3.5 py-2.5 text-right font-medium">占比</th>
                <th className="px-3.5 py-2.5 text-right font-medium">
                  <button
                    type="button"
                    onClick={() => toggleSort('pv')}
                    className="inline-flex flex-row-reverse items-center gap-1 hover:text-foreground"
                  >
                    <span>PV</span>
                    <SortIcon active={sort === 'pv'} order={order} />
                  </button>
                </th>
                <th className="px-3.5 py-2.5 text-right font-medium">
                  <button
                    type="button"
                    onClick={() => toggleSort('requests')}
                    className="inline-flex flex-row-reverse items-center gap-1 hover:text-foreground"
                  >
                    <span>请求数</span>
                    <SortIcon active={sort === 'requests'} order={order} />
                  </button>
                </th>
                <th className="px-3.5 py-2.5 text-right font-medium">
                  <button
                    type="button"
                    onClick={() => toggleSort('bytes')}
                    className="inline-flex flex-row-reverse items-center gap-1 hover:text-foreground"
                  >
                    <span>流量</span>
                    <SortIcon active={sort === 'bytes'} order={order} />
                  </button>
                </th>
                <th className="px-3.5 py-2.5 text-right font-medium">
                  <button
                    type="button"
                    onClick={() => toggleSort('err_count')}
                    className="inline-flex flex-row-reverse items-center gap-1 hover:text-foreground"
                  >
                    <span>错误数</span>
                    <SortIcon active={sort === 'err_count'} order={order} />
                  </button>
                </th>
                <th className="px-4 py-2.5 text-right font-medium">
                  <button
                    type="button"
                    onClick={() => toggleSort('avg_ms')}
                    className="inline-flex flex-row-reverse items-center gap-1 hover:text-foreground"
                  >
                    <span>均耗</span>
                    <SortIcon active={sort === 'avg_ms'} order={order} />
                  </button>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {isLoading && !data ? (
                <tr>
                  <td colSpan={7} className="px-4 py-8 text-center text-muted-foreground">
                    加载中…
                  </td>
                </tr>
              ) : (data?.rows.length ?? 0) === 0 ? (
                <tr>
                  <td colSpan={7} className="px-4 py-8 text-center text-muted-foreground">
                    当前筛选条件下暂无明细记录
                  </td>
                </tr>
              ) : (
                data?.rows.map((r) => (
                  <tr key={r.key} className="hover:bg-muted/30">
                    <td className="max-w-xs truncate px-4 py-2 font-mono" title={r.key}>
                      {r.key}
                    </td>
                    <td className="whitespace-nowrap px-3.5 py-2 text-right">
                      <div className="inline-flex items-center gap-2">
                        <div className="h-1.5 w-16 overflow-hidden rounded-full bg-muted">
                          <div
                            className="h-full rounded-full bg-run/75"
                            style={{ width: `${Math.min(100, Math.max(2, r.share))}%` }}
                          />
                        </div>
                        <span className="w-11 text-right font-mono tabular-nums text-muted-foreground">
                          {r.share.toFixed(1)}%
                        </span>
                      </div>
                    </td>
                    <td className="whitespace-nowrap px-3.5 py-2 text-right font-mono font-medium tabular-nums">
                      {formatNumber(r.pv)}
                    </td>
                    <td className="whitespace-nowrap px-3.5 py-2 text-right font-mono tabular-nums text-muted-foreground">
                      {formatNumber(r.requests)}
                    </td>
                    <td className="whitespace-nowrap px-3.5 py-2 text-right font-mono tabular-nums">
                      {formatBytes(r.bytes)}
                    </td>
                    <td
                      className={cn(
                        'whitespace-nowrap px-3.5 py-2 text-right font-mono tabular-nums',
                        r.err_count > 0 ? 'text-bad' : 'text-muted-foreground',
                      )}
                    >
                      {formatNumber(r.err_count)}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2 text-right font-mono tabular-nums text-muted-foreground">
                      {formatMs(r.avg_ms)}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}

      {pages > 1 && (
        <div className="flex items-center justify-between border-t px-5 py-3 text-xs text-muted-foreground">
          <span className="font-mono">
            共 {formatNumber(total)} 条 · 第 {page + 1} / {pages} 页
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

export function AnalyticsView() {
  const router = useRouter()
  const searchParams = useSearchParams()
  const { token } = useAuth()

  const initialSite = searchParams.get('site') || '_all'
  const [site, setSite] = useState(initialSite)
  const [range, setRange] = useState<AnalyticsRangeName>('7d')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')

  const activeSite = searchParams.get('site') || site
  const rangeQuery = useMemo(
    () => ({
      range: from || to ? undefined : range,
      from: from || undefined,
      to: to || undefined,
    }),
    [range, from, to],
  )

  const sites = useSites()
  const overview = useAnalyticsOverview(rangeQuery)
  const siteReport = useAnalyticsSite(
    activeSite !== '_all' ? activeSite : null,
    rangeQuery,
  )

  function selectSite(next: string) {
    setSite(next)
    if (next === '_all') {
      router.replace('/analytics')
    } else {
      router.replace(`/analytics?site=${encodeURIComponent(next)}`)
    }
  }

  function applyPreset(r: AnalyticsRangeName) {
    setRange(r)
    setFrom('')
    setTo('')
  }

  const isAll = activeSite === '_all'
  const activeQuery = isAll ? overview : siteReport
  const botSummary = useAnalyticsDimensions({
    site: activeSite,
    dim: 'bot',
    ...rangeQuery,
    sort: 'requests',
    order: 'desc',
    limit: 8,
    offset: 0,
  })

  if (sites.error) return <ErrorState error={sites.error} onRetry={() => sites.mutate()} />
  if (activeQuery.error) {
    return <ErrorState error={activeQuery.error} onRetry={() => activeQuery.mutate()} />
  }
  if (!activeQuery.data && activeQuery.isLoading) return <PageSkeleton />

  const enabled = isAll
    ? (overview.data?.enabled ?? true)
    : (siteReport.data?.enabled ?? true)

  const summary = isAll ? overview.data?.summary : siteReport.data?.summary
  const dailyRows = isAll ? (overview.data?.series ?? []) : (siteReport.data?.daily ?? [])
  const timezone = isAll
    ? (overview.data?.timezone ?? 'UTC')
    : (siteReport.data?.timezone ?? 'UTC')

  const selectedSiteMeta = sites.data?.find((s) => s.name === activeSite)
  const websiteHref = safeHttpUrl(selectedSiteMeta?.url)

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">
            {isAll ? '综合访问统计' : `${activeSite} · 站点统计`}
          </h1>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
            <span>统计时区: {timezone}</span>
            {!isAll && websiteHref && (
              <a
                href={websiteHref}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1 hover:text-foreground"
              >
                <GlobeIcon className="size-3.5" />
                {siteUrlLabel(websiteHref)}
                <ExternalLinkIcon className="size-3" />
              </a>
            )}
            {!isAll && (
              <Link
                href={`/site?name=${encodeURIComponent(activeSite)}`}
                className="hover:text-foreground underline-offset-4 hover:underline"
              >
                查看部署记录 →
              </Link>
            )}
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <select
            value={activeSite}
            onChange={(e) => selectSite(e.target.value)}
            aria-label="选择站点"
            className="h-8 rounded-lg border bg-card px-2.5 text-xs text-foreground outline-none focus-visible:border-ring"
          >
            <option value="_all">全部站点（综合）</option>
            {sites.data?.map((s) => (
              <option key={s.name} value={s.name}>
                {s.name}
                {s.url ? ` (${siteUrlLabel(s.url)})` : ''}
              </option>
            ))}
          </select>

          <div className="flex rounded-full border bg-muted/40 p-0.5 text-xs">
            {RANGES.map((r) => {
              const active = !from && !to && range === r.value
              return (
                <button
                  key={r.value}
                  type="button"
                  onClick={() => applyPreset(r.value)}
                  className={cn(
                    'rounded-full px-2.5 py-1 transition-colors',
                    active
                      ? 'bg-background font-medium text-foreground shadow-xs'
                      : 'text-muted-foreground hover:text-foreground',
                  )}
                >
                  {r.label}
                </button>
              )
            })}
          </div>

          <div className="flex items-center gap-1 text-xs">
            <input
              type="date"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              aria-label="开始日期"
              className="h-8 rounded-lg border bg-card px-2 font-mono text-xs"
            />
            <span className="text-muted-foreground">~</span>
            <input
              type="date"
              value={to}
              onChange={(e) => setTo(e.target.value)}
              aria-label="结束日期"
              className="h-8 rounded-lg border bg-card px-2 font-mono text-xs"
            />
          </div>

          <Button
            variant="outline"
            size="icon-sm"
            onClick={() => activeQuery.mutate()}
            title="刷新数据"
          >
            <RefreshCwIcon className="size-3.5" />
          </Button>
        </div>
      </div>

      {!enabled ? (
        <div className="rounded-2xl border border-dashed bg-card p-6 space-y-3">
          <h2 className="text-base font-semibold">站点统计尚未启用</h2>
          <p className="text-sm text-muted-foreground">
            在 <code className="font-mono text-xs">config.yaml</code> 中启用{' '}
            <code className="font-mono text-xs">analytics.enabled: true</code> 并配置 Caddy JSON
            访问日志路径（<code className="font-mono text-xs">analytics.caddy_logs</code>）后重启{' '}
            <code className="font-mono text-xs">deployd</code>，即可自动采集各站点的 DAU、PV、路径、来源与异常数据。
          </p>
        </div>
      ) : (
        <>
          {summary && <SummaryCards summary={summary} isAll={isAll} />}

          {isAll ? (
            <DailyTrendChart series={dailyRows} />
          ) : (
            <div className="grid gap-4 lg:grid-cols-3">
              <div className="lg:col-span-2">
                <DailyTrendChart series={dailyRows} />
              </div>
              <HourlyBarChart hourly={siteReport.data?.hourly ?? []} />
            </div>
          )}

          {isAll && overview.data && (
            <SitesCompareTable
              sites={overview.data.sites}
              onSelectSite={selectSite}
              onExport={() =>
                downloadFile(
                  `/api/analytics/export${buildQuery({ type: 'overview', ...rangeQuery })}`,
                  token,
                  'analytics-overview.csv',
                )
              }
            />
          )}

          <DailyBreakdownTable
            rows={dailyRows}
            title={isAll ? '全站按日汇总表' : `${activeSite} · 按日明细表`}
            onExport={() =>
              downloadFile(
                `/api/analytics/export${buildQuery({ type: 'daily', site: activeSite, ...rangeQuery })}`,
                token,
                `analytics-daily-${activeSite}.csv`,
              )
            }
          />

          {(summary?.bot_hits ?? 0) > 0 && (botSummary.data?.rows.length ?? 0) > 0 && (
            <BotBreakdownChart
              rows={botSummary.data?.rows ?? []}
              totalBotHits={summary?.bot_hits ?? 0}
            />
          )}

          <DimensionBreakdownTable
            site={activeSite}
            range={range}
            from={from}
            to={to}
          />
        </>
      )}
    </div>
  )
}

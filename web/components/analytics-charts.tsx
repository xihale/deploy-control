'use client'

import { useState } from 'react'
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  ComposedChart,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { formatBytes, formatNumber } from '@/lib/format'
import type {
  AnalyticsDailyPoint,
  AnalyticsDimensionRow,
  AnalyticsHourlyPoint,
} from '@/lib/types'

function DailyTooltip({
  active,
  payload,
  mode,
}: {
  active?: boolean
  payload?: Array<{ payload: AnalyticsDailyPoint }>
  mode: 'traffic' | 'requests'
}) {
  if (!active || !payload?.length) return null
  const d = payload[0].payload
  return (
    <div className="rounded-xl border bg-popover/95 px-3 py-2 text-xs text-popover-foreground shadow-md backdrop-blur-sm">
      <div className="mb-1.5 font-mono font-semibold">{d.day}</div>
      <div className="space-y-1 font-mono tabular-nums">
        {mode === 'traffic' ? (
          <>
            <div className="flex items-center justify-between gap-4">
              <span className="flex items-center gap-1.5 text-muted-foreground">
                <span className="size-2 rounded-full bg-ok" />
                日活 (DAU)
              </span>
              <span className="font-semibold text-foreground">{formatNumber(d.dau)}</span>
            </div>
            <div className="flex items-center justify-between gap-4">
              <span className="flex items-center gap-1.5 text-muted-foreground">
                <span className="size-2 rounded-xs bg-run" />
                浏览量 (PV)
              </span>
              <span className="font-semibold text-foreground">{formatNumber(d.pv)}</span>
            </div>
            <div className="flex items-center justify-between gap-4 text-muted-foreground">
              <span>总请求 / Bot</span>
              <span>
                {formatNumber(d.requests)} / {formatNumber(d.bot_hits)}
              </span>
            </div>
          </>
        ) : (
          <>
            <div className="flex items-center justify-between gap-4">
              <span className="flex items-center gap-1.5 text-muted-foreground">
                <span className="size-2 rounded-xs bg-run" />
                总请求
              </span>
              <span className="font-semibold text-foreground">{formatNumber(d.requests)}</span>
            </div>
            <div className="flex items-center justify-between gap-4">
              <span className="flex items-center gap-1.5 text-muted-foreground">
                <span className="size-2 rounded-full bg-wait" />
                流量
              </span>
              <span className="font-semibold text-foreground">{formatBytes(d.bytes)}</span>
            </div>
            <div className="flex items-center justify-between gap-4 text-muted-foreground">
              <span>4xx / 5xx</span>
              <span>
                {d.err_4xx} / {d.err_5xx}
              </span>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

export function DailyTrendChart({ series }: { series: AnalyticsDailyPoint[] }) {
  const [mode, setMode] = useState<'traffic' | 'requests'>('traffic')

  if (series.length === 0) {
    return (
      <div className="flex h-64 items-center justify-center rounded-2xl border bg-card text-sm text-muted-foreground">
        暂无趋势数据
      </div>
    )
  }

  return (
    <section className="rounded-2xl border bg-card p-3.5 sm:p-5">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2.5 sm:gap-3">
        <div className="flex flex-wrap items-center gap-3 text-xs text-muted-foreground sm:gap-4">
          <span className="text-sm font-semibold text-foreground">访问趋势</span>
          {mode === 'traffic' ? (
            <>
              <span className="flex items-center gap-1.5">
                <span className="size-2 rounded-full bg-ok" />
                日活 (DAU)
              </span>
              <span className="flex items-center gap-1.5">
                <span className="size-2 rounded-xs bg-run/70" />
                浏览量 (PV)
              </span>
            </>
          ) : (
            <>
              <span className="flex items-center gap-1.5">
                <span className="size-2 rounded-xs bg-run/70" />
                总请求
              </span>
              <span className="flex items-center gap-1.5">
                <span className="size-2 rounded-full bg-wait" />
                流量 (Bytes)
              </span>
            </>
          )}
        </div>

        <div className="flex shrink-0 rounded-full border bg-muted/50 p-0.5 text-xs">
          <button
            type="button"
            onClick={() => setMode('traffic')}
            className={`whitespace-nowrap rounded-full px-2.5 py-1 transition-colors ${
              mode === 'traffic'
                ? 'bg-background font-medium text-foreground shadow-xs'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            日活 & PV
          </button>
          <button
            type="button"
            onClick={() => setMode('requests')}
            className={`whitespace-nowrap rounded-full px-2.5 py-1 transition-colors ${
              mode === 'requests'
                ? 'bg-background font-medium text-foreground shadow-xs'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            请求 & 流量
          </button>
        </div>
      </div>

      <div className="h-52 w-full sm:h-56">
        <ResponsiveContainer width="100%" height="100%" minWidth={0}>
          <ComposedChart
            data={series}
            margin={{ top: 8, right: 2, left: -4, bottom: 0 }}
          >
            <CartesianGrid
              stroke="var(--border)"
              strokeDasharray="3 3"
              vertical={false}
            />
            <XAxis
              dataKey="day"
              tickFormatter={(v: string) => v.slice(5)}
              tick={{ fontSize: 11, fill: 'var(--muted-foreground)' }}
              axisLine={false}
              tickLine={false}
              minTickGap={24}
            />
            <YAxis
              yAxisId="left"
              width={32}
              tick={{ fontSize: 11, fill: 'var(--muted-foreground)' }}
              axisLine={false}
              tickLine={false}
              allowDecimals={false}
            />
            <YAxis
              yAxisId="right"
              orientation="right"
              tickFormatter={(v: number) =>
                mode === 'traffic' ? formatNumber(v) : formatBytes(v)
              }
              tick={{ fontSize: 11, fill: 'var(--muted-foreground)' }}
              axisLine={false}
              tickLine={false}
              width={mode === 'traffic' ? 34 : 48}
              allowDecimals={false}
            />
            <Tooltip
              cursor={{ fill: 'var(--muted)', fillOpacity: 0.35 }}
              content={<DailyTooltip mode={mode} />}
            />
            <Bar
              yAxisId="left"
              dataKey={mode === 'traffic' ? 'pv' : 'requests'}
              fill="var(--run)"
              fillOpacity={0.35}
              radius={[4, 4, 0, 0]}
              maxBarSize={28}
            />
            <Line
              yAxisId="right"
              type="monotone"
              dataKey={mode === 'traffic' ? 'dau' : 'bytes'}
              stroke={mode === 'traffic' ? 'var(--ok)' : 'var(--wait)'}
              strokeWidth={2.2}
              dot={series.length <= 31 ? { r: 2.5, strokeWidth: 0, fill: mode === 'traffic' ? 'var(--ok)' : 'var(--wait)' } : false}
              activeDot={{ r: 4.5 }}
            />
          </ComposedChart>
        </ResponsiveContainer>
      </div>
    </section>
  )
}

function HourlyTooltip({
  active,
  payload,
}: {
  active?: boolean
  payload?: Array<{ payload: AnalyticsHourlyPoint }>
}) {
  if (!active || !payload?.length) return null
  const h = payload[0].payload
  return (
    <div className="rounded-xl border bg-popover/95 px-3 py-2 text-xs text-popover-foreground shadow-md backdrop-blur-sm">
      <div className="mb-1 font-mono font-semibold">
        {h.hour.slice(5).replace('T', ' ')}:00
      </div>
      <div className="space-y-0.5 font-mono tabular-nums">
        <div>PV: {formatNumber(h.pv)}</div>
        <div>请求: {formatNumber(h.requests)}</div>
        <div>流量: {formatBytes(h.bytes)}</div>
        {(h.err_4xx > 0 || h.err_5xx > 0) && (
          <div className="text-bad">
            异常: {h.err_4xx}/{h.err_5xx}
          </div>
        )}
      </div>
    </div>
  )
}

export function HourlyBarChart({ hourly }: { hourly: AnalyticsHourlyPoint[] }) {
  if (hourly.length === 0) return null

  return (
    <section className="rounded-2xl border bg-card p-3.5 sm:p-5">
      <div className="mb-4 flex items-center justify-between gap-2">
        <span className="text-sm font-semibold">近 24 小时分时 PV</span>
        <span className="text-xs text-muted-foreground">按小时聚合</span>
      </div>

      <div className="h-52 w-full sm:h-56">
        <ResponsiveContainer width="100%" height="100%" minWidth={0}>
          <BarChart
            data={hourly}
            margin={{ top: 8, right: 4, left: -6, bottom: 0 }}
          >
            <CartesianGrid
              stroke="var(--border)"
              strokeDasharray="3 3"
              vertical={false}
            />
            <XAxis
              dataKey="hour"
              tickFormatter={(v: string) => `${v.slice(11)}h`}
              tick={{ fontSize: 11, fill: 'var(--muted-foreground)' }}
              axisLine={false}
              tickLine={false}
              interval={5}
            />
            <YAxis
              width={32}
              tick={{ fontSize: 11, fill: 'var(--muted-foreground)' }}
              axisLine={false}
              tickLine={false}
              allowDecimals={false}
            />
            <Tooltip
              cursor={{ fill: 'var(--muted)', fillOpacity: 0.35 }}
              content={<HourlyTooltip />}
            />
            <Bar dataKey="pv" radius={[3, 3, 0, 0]} maxBarSize={16}>
              {hourly.map((entry) => (
                <Cell
                  key={entry.hour}
                  fill={entry.err_5xx > 0 ? 'var(--bad)' : 'var(--ok)'}
                  fillOpacity={0.75}
                />
              ))}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </div>
    </section>
  )
}

function BotTooltip({
  active,
  payload,
}: {
  active?: boolean
  payload?: Array<{ payload: AnalyticsDimensionRow }>
}) {
  if (!active || !payload?.length) return null
  const r = payload[0].payload
  return (
    <div className="rounded-xl border bg-popover/95 px-3 py-2 text-xs text-popover-foreground shadow-md backdrop-blur-sm">
      <div className="mb-1 font-mono font-semibold">{r.key}</div>
      <div className="space-y-0.5 font-mono tabular-nums">
        <div>请求: {formatNumber(r.requests)} ({r.share.toFixed(1)}%)</div>
        <div>流量: {formatBytes(r.bytes)}</div>
        <div>异常: {formatNumber(r.err_count)}</div>
      </div>
    </div>
  )
}

export function BotBreakdownChart({
  rows,
  totalBotHits,
}: {
  rows: AnalyticsDimensionRow[]
  totalBotHits: number
}) {
  if (rows.length === 0) return null

  return (
    <section className="rounded-2xl border bg-card p-3.5 sm:p-5">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 className="text-sm font-semibold">爬虫 / Bot 流量构成 (Top 8)</h2>
          <p className="text-xs text-muted-foreground">
            已按 User-Agent 规则识别并从人类 DAU/PV 中剔除 · 共 {formatNumber(totalBotHits)} 次请求
          </p>
        </div>
      </div>

      <div className="h-60 w-full sm:h-56">
        <ResponsiveContainer width="100%" height="100%" minWidth={0}>
          <BarChart
            data={rows.slice(0, 8)}
            layout="vertical"
            margin={{ top: 4, right: 12, left: 0, bottom: 0 }}
          >
            <CartesianGrid
              stroke="var(--border)"
              strokeDasharray="3 3"
              horizontal={false}
            />
            <XAxis
              type="number"
              tick={{ fontSize: 11, fill: 'var(--muted-foreground)' }}
              axisLine={false}
              tickLine={false}
              allowDecimals={false}
            />
            <YAxis
              type="category"
              dataKey="key"
              width={116}
              tickFormatter={(v: string) => (v.length > 16 ? `${v.slice(0, 15)}…` : v)}
              tick={{ fontSize: 11, fill: 'var(--muted-foreground)' }}
              axisLine={false}
              tickLine={false}
            />
            <Tooltip
              cursor={{ fill: 'var(--muted)', fillOpacity: 0.35 }}
              content={<BotTooltip />}
            />
            <Bar
              dataKey="requests"
              fill="var(--wait)"
              fillOpacity={0.78}
              radius={[0, 4, 4, 0]}
              maxBarSize={18}
            />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </section>
  )
}

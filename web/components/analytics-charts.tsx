'use client'

import { useState } from 'react'
import { formatBytes, formatNumber } from '@/lib/format'
import type { AnalyticsDailyPoint, AnalyticsHourlyPoint } from '@/lib/types'

export function DailyTrendChart({ series }: { series: AnalyticsDailyPoint[] }) {
  const [hoverIdx, setHoverIdx] = useState<number | null>(null)
  const [mode, setMode] = useState<'traffic' | 'requests'>('traffic')

  if (series.length === 0) {
    return (
      <div className="flex h-56 items-center justify-center rounded-2xl border bg-card text-sm text-muted-foreground">
        暂无趋势数据
      </div>
    )
  }

  const width = 760
  const height = 220
  const padL = 44
  const padR = 44
  const padT = 18
  const padB = 28
  const plotW = width - padL - padR
  const plotH = height - padT - padB

  const maxPrimary = Math.max(
    1,
    ...series.map((d) => (mode === 'traffic' ? d.pv : d.requests)),
  )
  const maxSecondary = Math.max(
    1,
    ...series.map((d) => (mode === 'traffic' ? d.dau : d.bytes)),
  )

  const n = series.length
  const slotW = plotW / n
  const barW = Math.max(3, Math.min(26, slotW * 0.56))

  const xCenter = (i: number) => padL + (i + 0.5) * slotW
  const yPrimary = (v: number) => padT + plotH - (v / maxPrimary) * plotH
  const ySecondary = (v: number) => padT + plotH - (v / maxSecondary) * plotH

  const linePoints = series
    .map((d, i) => {
      const val = mode === 'traffic' ? d.dau : d.bytes
      return `${xCenter(i).toFixed(1)},${ySecondary(val).toFixed(1)}`
    })
    .join(' ')

  const active = hoverIdx !== null ? series[hoverIdx] : series[series.length - 1]
  const labelStep = Math.max(1, Math.ceil(n / 7))

  return (
    <section className="rounded-2xl border bg-card p-5">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div className="space-y-1">
          <div className="flex items-center gap-3 text-xs text-muted-foreground">
            <span className="font-mono font-medium text-foreground">
              {active.day}
            </span>
            {mode === 'traffic' ? (
              <>
                <span className="flex items-center gap-1.5">
                  <span className="size-2 rounded-full bg-ok" />
                  日活 <strong className="font-mono text-foreground">{formatNumber(active.dau)}</strong>
                </span>
                <span className="flex items-center gap-1.5">
                  <span className="size-2 rounded-sm bg-run/70" />
                  PV <strong className="font-mono text-foreground">{formatNumber(active.pv)}</strong>
                </span>
                <span>
                  请求 <strong className="font-mono text-foreground">{formatNumber(active.requests)}</strong>
                </span>
              </>
            ) : (
              <>
                <span className="flex items-center gap-1.5">
                  <span className="size-2 rounded-sm bg-run/70" />
                  请求 <strong className="font-mono text-foreground">{formatNumber(active.requests)}</strong>
                </span>
                <span className="flex items-center gap-1.5">
                  <span className="size-2 rounded-full bg-wait" />
                  流量 <strong className="font-mono text-foreground">{formatBytes(active.bytes)}</strong>
                </span>
                <span>
                  4xx/5xx <strong className="font-mono text-foreground">{active.err_4xx}/{active.err_5xx}</strong>
                </span>
              </>
            )}
          </div>
        </div>

        <div className="flex rounded-full border bg-muted/50 p-0.5 text-xs">
          <button
            type="button"
            onClick={() => setMode('traffic')}
            className={`rounded-full px-2.5 py-1 transition-colors ${
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
            className={`rounded-full px-2.5 py-1 transition-colors ${
              mode === 'requests'
                ? 'bg-background font-medium text-foreground shadow-xs'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            请求 & 流量
          </button>
        </div>
      </div>

      <div className="relative w-full overflow-hidden">
        <svg
          viewBox={`0 0 ${width} ${height}`}
          className="h-52 w-full select-none"
          onMouseLeave={() => setHoverIdx(null)}
        >
          {[0, 0.25, 0.5, 0.75, 1].map((t) => {
            const y = padT + plotH * (1 - t)
            const leftLabel = Math.round(maxPrimary * t)
            const rightLabel =
              mode === 'traffic'
                ? formatNumber(Math.round(maxSecondary * t))
                : formatBytes(Math.round(maxSecondary * t))
            return (
              <g key={t}>
                <line
                  x1={padL}
                  y1={y}
                  x2={width - padR}
                  y2={y}
                  stroke="currentColor"
                  strokeOpacity={0.08}
                  strokeDasharray={t > 0 && t < 1 ? '3 3' : undefined}
                />
                <text
                  x={padL - 6}
                  y={y + 3}
                  textAnchor="end"
                  className="fill-muted-foreground font-mono text-[10px]"
                >
                  {leftLabel}
                </text>
                <text
                  x={width - padR + 6}
                  y={y + 3}
                  textAnchor="start"
                  className="fill-muted-foreground font-mono text-[10px]"
                >
                  {rightLabel}
                </text>
              </g>
            )
          })}

          {series.map((d, i) => {
            const pVal = mode === 'traffic' ? d.pv : d.requests
            const y = yPrimary(pVal)
            const h = Math.max(1, padT + plotH - y)
            const isHover = hoverIdx === i
            return (
              <g key={d.day}>
                <rect
                  x={xCenter(i) - barW / 2}
                  y={y}
                  width={barW}
                  height={h}
                  rx={Math.min(3, barW / 2)}
                  fill="var(--run)"
                  fillOpacity={isHover ? 0.52 : 0.24}
                />
                <rect
                  x={padL + i * slotW}
                  y={padT}
                  width={slotW}
                  height={plotH}
                  fill="transparent"
                  onMouseEnter={() => setHoverIdx(i)}
                />
                {(i % labelStep === 0 || i === n - 1) && (
                  <text
                    x={xCenter(i)}
                    y={height - 8}
                    textAnchor="middle"
                    className="fill-muted-foreground font-mono text-[10px]"
                  >
                    {d.day.slice(5)}
                  </text>
                )}
              </g>
            )
          })}

          {n > 1 && (
            <polyline
              fill="none"
              stroke={mode === 'traffic' ? 'var(--ok)' : 'var(--wait)'}
              strokeWidth={2}
              strokeLinejoin="round"
              strokeLinecap="round"
              points={linePoints}
            />
          )}

          {series.map((d, i) => {
            const val = mode === 'traffic' ? d.dau : d.bytes
            const isHover = hoverIdx === i
            return (
              <circle
                key={`pt-${d.day}`}
                cx={xCenter(i)}
                cy={ySecondary(val)}
                r={isHover ? 4.5 : n <= 31 ? 2.5 : 1.5}
                fill={mode === 'traffic' ? 'var(--ok)' : 'var(--wait)'}
                className="pointer-events-none"
              />
            )
          })}
        </svg>
      </div>
    </section>
  )
}

export function HourlyBarChart({ hourly }: { hourly: AnalyticsHourlyPoint[] }) {
  const [hoverIdx, setHoverIdx] = useState<number | null>(null)

  if (hourly.length === 0) return null

  const width = 420
  const height = 220
  const padL = 36
  const padR = 10
  const padT = 18
  const padB = 28
  const plotW = width - padL - padR
  const plotH = height - padT - padB
  const maxPV = Math.max(1, ...hourly.map((h) => h.pv))
  const slotW = plotW / hourly.length
  const barW = Math.max(4, slotW * 0.65)
  const active = hoverIdx !== null ? hourly[hoverIdx] : hourly[hourly.length - 1]

  return (
    <section className="rounded-2xl border bg-card p-5">
      <div className="mb-4 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <span className="font-medium text-foreground">近 24 小时分时访问</span>
        <span className="font-mono">
          {active.hour.slice(5).replace('T', ' ')}:00 · PV{' '}
          <strong className="text-foreground">{formatNumber(active.pv)}</strong> · 请求{' '}
          <strong className="text-foreground">{formatNumber(active.requests)}</strong>
        </span>
      </div>

      <svg
        viewBox={`0 0 ${width} ${height}`}
        className="h-52 w-full select-none"
        onMouseLeave={() => setHoverIdx(null)}
      >
        {[0, 0.5, 1].map((t) => {
          const y = padT + plotH * (1 - t)
          return (
            <g key={t}>
              <line
                x1={padL}
                y1={y}
                x2={width - padR}
                y2={y}
                stroke="currentColor"
                strokeOpacity={0.08}
              />
              <text
                x={padL - 6}
                y={y + 3}
                textAnchor="end"
                className="fill-muted-foreground font-mono text-[10px]"
              >
                {Math.round(maxPV * t)}
              </text>
            </g>
          )
        })}

        {hourly.map((h, i) => {
          const xCenter = padL + (i + 0.5) * slotW
          const y = padT + plotH - (h.pv / maxPV) * plotH
          const bh = Math.max(1.5, padT + plotH - y)
          const isHover = hoverIdx === i
          return (
            <g key={h.hour}>
              <rect
                x={xCenter - barW / 2}
                y={y}
                width={barW}
                height={bh}
                rx={2}
                fill={h.err_5xx > 0 ? 'var(--bad)' : 'var(--ok)'}
                fillOpacity={isHover ? 0.95 : 0.65}
              />
              <rect
                x={padL + i * slotW}
                y={padT}
                width={slotW}
                height={plotH}
                fill="transparent"
                onMouseEnter={() => setHoverIdx(i)}
              />
              {i % 6 === 0 && (
                <text
                  x={xCenter}
                  y={height - 8}
                  textAnchor="middle"
                  className="fill-muted-foreground font-mono text-[10px]"
                >
                  {h.hour.slice(11)}h
                </text>
              )}
            </g>
          )
        })}
      </svg>
    </section>
  )
}

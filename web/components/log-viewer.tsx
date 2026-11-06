'use client'

import { useEffect, useRef, useState } from 'react'
import { ArrowDownToLineIcon, CheckIcon, CopyIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useDeployLog } from '@/hooks/use-api'
import { isActive } from '@/lib/status'
import type { DeployJob } from '@/lib/types'

const STEP = 64 * 1024

export function LogViewer({ job }: { job: DeployJob }) {
  const active = isActive(job.status)
  const [bytes, setBytes] = useState(STEP)
  const [copied, setCopied] = useState(false)
  const { data, isLoading } = useDeployLog(job.id, bytes, active)
  const scroller = useRef<HTMLDivElement>(null)
  const pinned = useRef(true)

  useEffect(() => {
    const el = scroller.current
    if (el && pinned.current) el.scrollTop = el.scrollHeight
  }, [data])

  function onScroll() {
    const el = scroller.current
    if (!el) return
    pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24
  }

  async function copy() {
    if (!data) return
    await navigator.clipboard.writeText(data)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  const truncated = !!data && data.length >= bytes

  return (
    <section className="overflow-hidden rounded-2xl border bg-[oklch(0.17_0.005_260)] text-[oklch(0.9_0.01_260)]">
      <div className="flex items-center justify-between border-b border-white/10 px-4 py-2.5">
        <div className="flex items-center gap-1.5" aria-hidden>
          <span className="size-2.5 rounded-full bg-white/15" />
          <span className="size-2.5 rounded-full bg-white/15" />
          <span className="size-2.5 rounded-full bg-white/15" />
        </div>
        <span className="font-mono text-xs text-white/40">
          {active ? '实时' : `job-${job.id}.log`}
        </span>
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={copy}
          disabled={!data}
          aria-label="复制日志"
          className="text-white/60 hover:bg-white/10 hover:text-white"
        >
          {copied ? <CheckIcon /> : <CopyIcon />}
        </Button>
      </div>

      <div
        ref={scroller}
        onScroll={onScroll}
        className="max-h-[28rem] min-h-40 overflow-auto p-4"
        tabIndex={0}
        role="log"
        aria-label="部署日志"
      >
        {isLoading ? (
          <div className="space-y-2" aria-busy="true">
            {[70, 45, 60].map((w) => (
              <div
                key={w}
                className="h-3.5 animate-pulse rounded bg-white/10"
                style={{ width: `${w}%` }}
              />
            ))}
          </div>
        ) : data === null || data === undefined ? (
          <p className="py-10 text-center font-mono text-xs text-white/40">
            {active ? '等待执行…' : '暂无日志'}
          </p>
        ) : (
          <>
            {truncated && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setBytes((b) => b + STEP * 4)}
                className="mb-3 text-white/60 hover:bg-white/10 hover:text-white"
              >
                <ArrowDownToLineIcon />
                显示更早的内容
              </Button>
            )}
            <pre className="font-mono text-xs leading-relaxed whitespace-pre-wrap break-all">
              {data}
            </pre>
          </>
        )}
      </div>
    </section>
  )
}

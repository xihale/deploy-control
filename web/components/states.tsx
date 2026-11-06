'use client'

import { RefreshCwIcon, TriangleAlertIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { ApiError } from '@/lib/api'

export function ErrorState({
  error,
  onRetry,
}: {
  error: unknown
  onRetry?: () => void
}) {
  const message =
    error instanceof ApiError
      ? error.status === 403
        ? '没有权限'
        : error.status === 404
          ? '未找到'
          : error.message
      : '无法连接到服务'
  return (
    <div className="flex flex-col items-center gap-4 py-24 text-muted-foreground">
      <TriangleAlertIcon className="size-10 text-bad" strokeWidth={1.25} />
      <p className="text-sm">{message}</p>
      {onRetry && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RefreshCwIcon />
          重试
        </Button>
      )}
    </div>
  )
}

export function PageSkeleton() {
  return (
    <div className="space-y-8" aria-busy="true" aria-label="加载中">
      <div className="space-y-5">
        <div className="h-9 w-28 animate-pulse rounded-lg bg-muted" />
        <div className="h-2.5 animate-pulse rounded-full bg-muted" />
      </div>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {Array.from({ length: 3 }).map((_, i) => (
          <div key={i} className="h-40 animate-pulse rounded-2xl bg-muted" />
        ))}
      </div>
    </div>
  )
}

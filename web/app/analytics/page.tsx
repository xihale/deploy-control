import { Suspense } from 'react'
import type { Metadata } from 'next'
import { AnalyticsView } from '@/components/analytics-view'

export const metadata: Metadata = { title: '访问统计 · 部署控制台' }

export default function AnalyticsPage() {
  return (
    <Suspense>
      <AnalyticsView />
    </Suspense>
  )
}

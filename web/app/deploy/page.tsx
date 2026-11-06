import { Suspense } from 'react'
import type { Metadata } from 'next'
import { DeployView } from '@/components/deploy-view'

export const metadata: Metadata = { title: '部署详情 · 部署控制台' }

export default function DeployPage() {
  return (
    <Suspense>
      <DeployView />
    </Suspense>
  )
}

import { Suspense } from 'react'
import type { Metadata } from 'next'
import { SiteView } from '@/components/site-view'

export const metadata: Metadata = { title: '站点 · 部署控制台' }

export default function SitePage() {
  return (
    <Suspense>
      <SiteView />
    </Suspense>
  )
}

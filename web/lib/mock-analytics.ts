import type {
  AnalyticsDailyPoint,
  AnalyticsDateRange,
  AnalyticsDimName,
  AnalyticsDimension,
  AnalyticsDimensionRow,
  AnalyticsHourlyPoint,
  AnalyticsOverview,
  AnalyticsSiteReport,
  AnalyticsSiteRow,
  Site,
} from './types'

function resolveDemoRange(url: URL): AnalyticsDateRange {
  const rangeParam = url.searchParams.get('range') || '7d'
  const fromParam = url.searchParams.get('from') || ''
  const toParam = url.searchParams.get('to') || ''
  const now = new Date()
  const fmt = (d: Date) =>
    `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`

  if (fromParam || toParam) {
    const to = toParam || fmt(now)
    const toD = new Date(to + 'T00:00:00')
    const fromD = fromParam
      ? new Date(fromParam + 'T00:00:00')
      : new Date(toD.getTime() - 6 * 86400_000)
    const days = Math.max(1, Math.round((toD.getTime() - fromD.getTime()) / 86400_000) + 1)
    return { from: fmt(fromD), to: fmt(toD), days }
  }

  const days =
    rangeParam === 'today' ? 1 : rangeParam === '30d' ? 30 : rangeParam === '90d' ? 90 : 7
  const fromD = new Date(now.getTime() - (days - 1) * 86400_000)
  return { from: fmt(fromD), to: fmt(now), days }
}

function listDays(dr: AnalyticsDateRange): string[] {
  const out: string[] = []
  const start = new Date(dr.from + 'T00:00:00')
  for (let i = 0; i < dr.days; i++) {
    const d = new Date(start.getTime() + i * 86400_000)
    out.push(
      `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`,
    )
  }
  return out
}

function siteWeight(name: string): number {
  switch (name) {
    case 'blog':
      return 1.0
    case 'zig-playground':
      return 0.68
    case 'poetry':
      return 0.52
    case 'schedmate':
      return 0.35
    default:
      return 0.18
  }
}

function dailyForSite(site: string, day: string): AnalyticsDailyPoint {
  const w = siteWeight(site)
  let h = 0
  for (let i = 0; i < day.length; i++) h = (h * 31 + day.charCodeAt(i)) % 997
  const wave = 0.72 + ((h % 55) / 100)
  const dau = Math.max(1, Math.round(78 * w * wave))
  const pv = Math.round(dau * (2.4 + (h % 9) * 0.15))
  const requests = Math.round(pv * (5.2 + (h % 4) * 0.4))
  const bytes = requests * (18500 + (h % 9000))
  const err_4xx = Math.round((h % 7) * w)
  const err_5xx = h % 29 === 0 ? 1 : 0
  const bot_hits = Math.round(pv * (0.45 + (h % 5) * 0.08))
  const avg_duration_ms = Number((6.4 + (h % 14) * 0.9 + (1 - w) * 4).toFixed(1))
  return {
    day,
    dau,
    pv,
    requests,
    bytes,
    err_4xx,
    err_5xx,
    bot_hits,
    avg_duration_ms,
    pv_per_uv: Number((pv / Math.max(1, dau)).toFixed(2)),
  }
}

export function mockAnalyticsRequest(sites: Site[], url: URL): Response | null {
  const p = url.pathname
  const dr = resolveDemoRange(url)
  const days = listDays(dr)

  if (p === '/api/analytics/overview') {
    const series: AnalyticsDailyPoint[] = days.map((day) => {
      const perSite = sites.map((s) => dailyForSite(s.name, day))
      const sumDau = perSite.reduce((a, b) => a + b.dau, 0)
      const dau = Math.round(sumDau * 0.86) // cross-site deduplication simulation
      const pv = perSite.reduce((a, b) => a + b.pv, 0)
      const requests = perSite.reduce((a, b) => a + b.requests, 0)
      const bytes = perSite.reduce((a, b) => a + b.bytes, 0)
      const err_4xx = perSite.reduce((a, b) => a + b.err_4xx, 0)
      const err_5xx = perSite.reduce((a, b) => a + b.err_5xx, 0)
      const bot_hits = perSite.reduce((a, b) => a + b.bot_hits, 0)
      const avg_duration_ms = Number(
        (perSite.reduce((a, b) => a + b.avg_duration_ms, 0) / Math.max(1, perSite.length)).toFixed(1),
      )
      return {
        day,
        dau,
        pv,
        requests,
        bytes,
        err_4xx,
        err_5xx,
        bot_hits,
        avg_duration_ms,
        pv_per_uv: Number((pv / Math.max(1, dau)).toFixed(2)),
      }
    })

    const siteRows: AnalyticsSiteRow[] = sites.map((s) => {
      const pts = days.map((d) => dailyForSite(s.name, d))
      const todayPt = dailyForSite(s.name, listDays({ ...dr, days: 1, from: dr.to, to: dr.to })[0])
      const yestDate = new Date(Date.now() - 86400_000)
      const yestStr = `${yestDate.getFullYear()}-${String(yestDate.getMonth() + 1).padStart(2, '0')}-${String(yestDate.getDate()).padStart(2, '0')}`
      const yestPt = dailyForSite(s.name, yestStr)
      const range_pv = pts.reduce((a, b) => a + b.pv, 0)
      const range_uv = Math.round(pts.reduce((a, b) => a + b.dau, 0) * (dr.days === 1 ? 1 : 0.58))
      const wau = Math.round(todayPt.dau * 4.2)
      const mau = Math.round(todayPt.dau * 11.8)
      return {
        site: s.name,
        url: s.url,
        hosts: s.url ? [s.url.replace(/^https?:\/\//, '')] : [],
        today_dau: todayPt.dau,
        yesterday_dau: yestPt.dau,
        wau,
        mau,
        range_uv,
        today_pv: todayPt.pv,
        range_pv,
        range_requests: pts.reduce((a, b) => a + b.requests, 0),
        range_bytes: pts.reduce((a, b) => a + b.bytes, 0),
        err_4xx: pts.reduce((a, b) => a + b.err_4xx, 0),
        err_5xx: pts.reduce((a, b) => a + b.err_5xx, 0),
        bot_hits: pts.reduce((a, b) => a + b.bot_hits, 0),
        avg_duration_ms: todayPt.avg_duration_ms,
        pv_per_uv: Number((range_pv / Math.max(1, range_uv)).toFixed(2)),
      }
    })

    const last = series[series.length - 1]
    const prev = series[series.length - 2] ?? last
    const range_pv = series.reduce((a, b) => a + b.pv, 0)
    const range_uv = Math.round(siteRows.reduce((a, b) => a + b.range_uv, 0) * 0.85)
    const rep: AnalyticsOverview = {
      enabled: true,
      timezone: 'Asia/Shanghai',
      range: dr,
      summary: {
        today_dau: last?.dau ?? 0,
        yesterday_dau: prev?.dau ?? 0,
        wau: Math.round((last?.dau ?? 0) * 4.1),
        mau: Math.round((last?.dau ?? 0) * 11.4),
        range_uv,
        today_pv: last?.pv ?? 0,
        range_pv,
        range_requests: series.reduce((a, b) => a + b.requests, 0),
        range_bytes: series.reduce((a, b) => a + b.bytes, 0),
        err_4xx: series.reduce((a, b) => a + b.err_4xx, 0),
        err_5xx: series.reduce((a, b) => a + b.err_5xx, 0),
        bot_hits: series.reduce((a, b) => a + b.bot_hits, 0),
        avg_duration_ms: last?.avg_duration_ms ?? 0,
        pv_per_uv: Number((range_pv / Math.max(1, range_uv)).toFixed(2)),
        engaged_rate: 61.4,
      },
      series,
      sites: siteRows,
    }
    return new Response(JSON.stringify(rep), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  const siteMatch = p.match(/^\/api\/analytics\/sites\/([^/]+)$/)
  if (siteMatch) {
    const siteName = decodeURIComponent(siteMatch[1])
    const site = sites.find((s) => s.name === siteName)
    if (!site) return new Response('unknown site', { status: 404 })
    const daily = days.map((d) => dailyForSite(site.name, d))
    const last = daily[daily.length - 1]
    const prev = daily[daily.length - 2] ?? last
    const now = new Date()
    const hourly: AnalyticsHourlyPoint[] = Array.from({ length: 24 }, (_, idx) => {
      const hDate = new Date(now.getTime() - (23 - idx) * 3600_000)
      const hh = `${hDate.getFullYear()}-${String(hDate.getMonth() + 1).padStart(2, '0')}-${String(hDate.getDate()).padStart(2, '0')}T${String(hDate.getHours()).padStart(2, '0')}`
      const mult = 0.4 + Math.sin((idx / 24) * Math.PI) * 0.9
      const pv = Math.max(1, Math.round((last.pv / 24) * mult))
      const requests = Math.round(pv * 5.3)
      return {
        hour: hh,
        pv,
        requests,
        bytes: requests * 21000,
        err_4xx: idx % 7 === 0 ? 1 : 0,
        err_5xx: 0,
      }
    })
    const range_pv = daily.reduce((a, b) => a + b.pv, 0)
    const range_uv = Math.round(daily.reduce((a, b) => a + b.dau, 0) * (dr.days === 1 ? 1 : 0.58))
    const rep: AnalyticsSiteReport = {
      enabled: true,
      timezone: 'Asia/Shanghai',
      site: site.name,
      url: site.url,
      hosts: site.url ? [site.url.replace(/^https?:\/\//, '')] : [],
      range: dr,
      summary: {
        today_dau: last.dau,
        yesterday_dau: prev.dau,
        wau: Math.round(last.dau * 4.2),
        mau: Math.round(last.dau * 11.8),
        range_uv,
        today_pv: last.pv,
        range_pv,
        range_requests: daily.reduce((a, b) => a + b.requests, 0),
        range_bytes: daily.reduce((a, b) => a + b.bytes, 0),
        err_4xx: daily.reduce((a, b) => a + b.err_4xx, 0),
        err_5xx: daily.reduce((a, b) => a + b.err_5xx, 0),
        bot_hits: daily.reduce((a, b) => a + b.bot_hits, 0),
        avg_duration_ms: last.avg_duration_ms,
        pv_per_uv: Number((range_pv / Math.max(1, range_uv)).toFixed(2)),
        engaged_rate: 64.2,
      },
      daily,
      hourly,
    }
    return new Response(JSON.stringify(rep), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  if (p === '/api/analytics/dimensions') {
    const site = url.searchParams.get('site') || '_all'
    const dim = (url.searchParams.get('dim') || 'path') as AnalyticsDimName
    const q = (url.searchParams.get('q') || '').trim().toLowerCase()
    const reqOnlyDim = dim === 'status' || dim === 'error_path' || dim === 'bot' || dim === 'bot_path'
    const sortBy = url.searchParams.get('sort') || (reqOnlyDim ? 'requests' : 'pv')
    const order = url.searchParams.get('order') || 'desc'
    const limit = Number(url.searchParams.get('limit') || 50)
    const offset = Number(url.searchParams.get('offset') || 0)
    const scale = site === '_all' ? 2.4 : siteWeight(site) * dr.days * 0.25

    const presets: Record<AnalyticsDimName, string[]> = {
      path: ['/', '/posts/zig-wasm-notes', '/posts/caddy-deploy-control', '/projects', '/about', '/archives', '/tags/go', '/tags/zig', '/rss.xml', '/search'],
      referrer: ['(direct)', 'github.com', 'news.ycombinator.com', 'google.com', 'bing.com', 'v2ex.com', 'x.com', 'lobste.rs'],
      browser: ['Chrome', 'Safari', 'Firefox', 'Edge', 'Opera', 'Other'],
      os: ['macOS', 'Windows', 'iOS', 'Android', 'Linux', 'Other'],
      device: ['Desktop', 'Mobile', 'Tablet'],
      status: ['200', '304', '301', '404', '206', '502'],
      error_path: ['404 /wp-login.php', '404 /favicon.png', '404 /posts/old-slug', '404 /.env', '502 /api/preview'],
      bot: ['Googlebot (Search)', 'GPTBot (AI)', 'Bytespider (AI)', 'curl (CLI)', 'Bingbot (Search)', 'ClaudeBot (AI)', 'UptimeRobot (Monitor)', 'Empty UA'],
      bot_path: ['/robots.txt', '/sitemap.xml', '/', '/posts/zig-wasm-notes', '/wp-login.php', '/.env', '/feed.xml'],
    }
    const keys = presets[dim] ?? presets.path
    let rows: AnalyticsDimensionRow[] = keys.map((key, idx) => {
      const base = Math.max(1, Math.round((160 / (idx + 1.2)) * Math.max(0.6, scale)))
      const isErr = dim === 'error_path' || (dim === 'status' && Number(key) >= 400)
      const pv = isErr || reqOnlyDim || key === '301' ? 0 : base
      const requests = isErr || reqOnlyDim ? base : Math.round(base * (key === '200' ? 4.2 : 1.15))
      const bytes = requests * (key === '200' || key === '/' ? 19400 : 8200)
      return {
        key,
        pv,
        requests,
        bytes,
        err_count: isErr ? requests : dim === 'bot_path' && key.includes('.env') ? requests : 0,
        avg_ms: Number((4.2 + idx * 1.7).toFixed(1)),
        share: 0,
      }
    })

    if (q) rows = rows.filter((r) => r.key.toLowerCase().includes(q))
    const total_pv = rows.reduce((a, b) => a + b.pv, 0)
    const total_requests = rows.reduce((a, b) => a + b.requests, 0)
    const total_bytes = rows.reduce((a, b) => a + b.bytes, 0)
    const denom = reqOnlyDim || total_pv === 0 ? total_requests : total_pv
    rows = rows.map((r) => ({
      ...r,
      share: denom > 0 ? Number((((reqOnlyDim || total_pv === 0 ? r.requests : r.pv) * 100) / denom).toFixed(2)) : 0,
    }))

    rows.sort((a, b) => {
      const dir = order === 'asc' ? 1 : -1
      if (sortBy === 'key') return a.key.localeCompare(b.key) * dir
      const av = (a as unknown as Record<string, number>)[sortBy] ?? a.pv
      const bv = (b as unknown as Record<string, number>)[sortBy] ?? b.pv
      return (av - bv) * dir
    })

    const rep: AnalyticsDimension = {
      enabled: true,
      site,
      dim,
      range: dr,
      total: rows.length,
      total_pv,
      total_requests,
      total_bytes,
      rows: rows.slice(offset, offset + limit),
    }
    return new Response(JSON.stringify(rep), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  if (p === '/api/analytics/export') {
    const csv = '\uFEFFKey,PV,Requests\n(demo),100,420\n'
    return new Response(csv, {
      status: 200,
      headers: {
        'Content-Type': 'text/csv; charset=utf-8',
        'Content-Disposition': `attachment; filename="analytics-demo-${dr.from}-to-${dr.to}.csv"`,
      },
    })
  }

  return null
}

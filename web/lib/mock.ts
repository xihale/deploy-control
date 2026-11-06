import type { DeployJob, DeployStatus, Site } from './types'

const SITES: Site[] = [
  {
    name: 'blog',
    repo: 'https://github.com/example/blog.git',
    branch: 'main',
    script: '/srv/deploy/blog.sh',
    has_webhook_secret: true,
  },
  {
    name: 'zig-playground',
    repo: 'https://github.com/example/zig-playground.git',
    branch: 'main',
    script: '/srv/deploy/zig-playground.sh',
    has_webhook_secret: true,
    run_as: 'zig-ci',
  },
  {
    name: 'poetry',
    repo: 'https://github.com/example/poetry.git',
    branch: 'master',
    script: '/srv/deploy/poetry.sh',
    has_webhook_secret: true,
  },
  {
    name: 'schedmate',
    repo: 'https://github.com/example/schedmate.git',
    branch: 'main',
    script: '/srv/deploy/schedmate.sh',
    has_webhook_secret: false,
  },
  {
    name: 'foyer',
    repo: 'https://github.com/example/foyer.git',
    branch: 'main',
    script: '/srv/deploy/foyer.sh',
    has_webhook_secret: true,
  },
]

const hex = (seed: number) => {
  let x = seed * 2654435761
  let out = ''
  while (out.length < 40) {
    x = (x * 1103515245 + 12345) % 2147483648
    out += x.toString(16).padStart(8, '0')
  }
  return out.slice(0, 40)
}

const PATTERNS: Record<string, DeployStatus[]> = {
  blog: ['succeeded', 'succeeded', 'succeeded', 'superseded', 'succeeded', 'succeeded', 'failed', 'succeeded', 'succeeded', 'succeeded', 'succeeded', 'succeeded'],
  'zig-playground': ['running', 'succeeded', 'succeeded', 'failed', 'failed', 'succeeded', 'succeeded', 'succeeded', 'superseded', 'succeeded'],
  poetry: ['failed', 'succeeded', 'succeeded', 'succeeded', 'succeeded', 'succeeded', 'succeeded', 'succeeded'],
  schedmate: ['queued', 'succeeded', 'succeeded', 'succeeded', 'failed', 'succeeded'],
  foyer: [],
}

let jobs: DeployJob[] = []

function seed() {
  const now = Date.now()
  let base = 0
  const all: DeployJob[] = []
  SITES.forEach((site, si) => {
    const pattern = PATTERNS[site.name] ?? []
    pattern.forEach((status, i) => {
      const id = base + pattern.length - i
      const created = now - (i * 7 + si * 2 + 1) * 3600_000 - si * 600_000
      const dur = 20_000 + ((id * 7919) % 90_000)
      const sha = hex(id)
      const job: DeployJob = {
        id,
        site: site.name,
        trigger_type: id % 5 === 0 ? 'manual' : 'push',
        branch: site.branch,
        sha: id % 5 === 0 ? undefined : sha,
        delivery_id: id % 5 === 0 ? undefined : `delivery-${id}`,
        status,
        attempts: 1,
        created_at: new Date(created).toISOString(),
      }
      if (status !== 'queued' && status !== 'superseded') {
        job.checked_sha = sha
        job.started_at = new Date(created + 3000).toISOString()
      }
      if (status === 'succeeded') {
        job.finished_at = new Date(created + 3000 + dur).toISOString()
        job.exit_code = 0
      }
      if (status === 'failed') {
        job.finished_at = new Date(created + 3000 + dur).toISOString()
        job.exit_code = 1
        job.error = 'deploy script exited with status 1'
      }
      if (status === 'running') {
        job.created_at = new Date(now - 95_000).toISOString()
        job.started_at = new Date(now - 90_000).toISOString()
      }
      if (status === 'queued') {
        job.created_at = new Date(now - 20_000).toISOString()
      }
      if (status !== 'queued' && status !== 'superseded') {
        job.log_path = `/var/lib/deploy-control/logs/job-${id}.log`
      }
      all.push(job)
    })
    base += pattern.length
  })
  return all.sort((a, b) => b.created_at.localeCompare(a.created_at))
}

function logFor(job: DeployJob) {
  const lines = [
    `[deploy] job #${job.id} site=${job.site} branch=${job.branch}`,
    `[git] fetch origin ${job.branch}`,
    `[git] checkout ${job.checked_sha ?? ''}`,
    '[build] installing dependencies…',
    '[build] resolved 312 packages',
    '[build] compiling…',
  ]
  if (job.status === 'running') return lines.join('\n') + '\n'
  if (job.status === 'failed') {
    return (
      lines.join('\n') +
      '\n[build] error: module not found: ./components/missing\n[deploy] script exited with status 1\n'
    )
  }
  return (
    lines.join('\n') +
    '\n[build] 24 pages generated\n[publish] syncing artifacts…\n[publish] switched current -> release\n[deploy] done\n'
  )
}

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function text(body: string, status = 200) {
  return new Response(body, {
    status,
    headers: { 'Content-Type': 'text/plain' },
  })
}

export async function mockRequest(path: string, init?: RequestInit) {
  await new Promise((r) => setTimeout(r, 180))
  if (jobs.length === 0) jobs = seed()
  const url = new URL(path, 'http://demo.local')
  const method = init?.method ?? 'GET'
  const p = url.pathname

  if (p === '/healthz') {
    return json({
      status: 'ok',
      queued: jobs.filter((j) => j.status === 'queued').length,
      running: jobs.filter((j) => j.status === 'running').length,
    })
  }
  if (p === '/api/sites') return json(SITES)
  if (p === '/api/me') return json({ name: 'demo', role: 'admin' })

  if (p === '/api/deploys' && method === 'POST') {
    const body = JSON.parse(String(init?.body ?? '{}'))
    const site = SITES.find((s) => s.name === body.site)
    if (!site) return text('unknown site', 400)
    const job: DeployJob = {
      id: Math.max(0, ...jobs.map((j) => j.id)) + 1,
      site: site.name,
      trigger_type: 'manual',
      branch: body.branch || site.branch,
      sha: body.sha || undefined,
      status: 'queued',
      attempts: 0,
      created_at: new Date().toISOString(),
    }
    jobs = [job, ...jobs]
    return json(job, 202)
  }

  if (p === '/api/deploys') {
    const site = url.searchParams.get('site')
    const status = url.searchParams.get('status')
    const limit = Number(url.searchParams.get('limit') ?? 20)
    const offset = Number(url.searchParams.get('offset') ?? 0)
    const filtered = jobs.filter(
      (j) => (!site || j.site === site) && (!status || j.status === status),
    )
    return json({ total: filtered.length, jobs: filtered.slice(offset, offset + limit) })
  }

  const m = p.match(/^\/api\/deploys\/(\d+)(\/log)?$/)
  if (m) {
    const job = jobs.find((j) => j.id === Number(m[1]))
    if (!job) return text('not found', 404)
    if (!m[2]) return json(job)
    if (!job.log_path) return text('log not found', 404)
    return text(logFor(job))
  }
  return text('not found', 404)
}

export const DEMO_TOKEN = 'demo'

const BASE = process.env.NEXT_PUBLIC_API_BASE ?? ''

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function send(path: string, token: string | null, init?: RequestInit) {
  if (token === DEMO_TOKEN) {
    const { mockRequest } = await import('./mock')
    return mockRequest(path, init)
  }
  const headers = new Headers(init?.headers)
  if (token) headers.set('Authorization', `Bearer ${token}`)
  return fetch(`${BASE}${path}`, { ...init, headers, cache: 'no-store' })
}

async function ensureOk(res: Response) {
  if (res.ok) return res
  const text = (await res.text().catch(() => '')).trim()
  throw new ApiError(res.status, text.slice(0, 300) || `HTTP ${res.status}`)
}

export async function getJson<T>(path: string, token: string | null): Promise<T> {
  const res = await ensureOk(await send(path, token))
  return res.json() as Promise<T>
}

// 日志在任务启动前返回 404，这里视为“暂无日志”
export async function getText(
  path: string,
  token: string | null,
): Promise<string | null> {
  const res = await send(path, token)
  if (res.status === 404) return null
  return (await ensureOk(res)).text()
}

export async function postJson<T>(
  path: string,
  token: string | null,
  body: unknown,
): Promise<T> {
  const res = await ensureOk(
    await send(path, token, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }),
  )
  return res.json() as Promise<T>
}

export function buildQuery(params: Record<string, string | number | undefined>) {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') q.set(k, String(v))
  }
  const s = q.toString()
  return s ? `?${s}` : ''
}

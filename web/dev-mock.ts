/**
 * Mock de 12v. para desarrollo — NUNCA en build.
 * Ruta: Vite middleware (VITE_MOCK=true). Implementa los endpoints del contrato
 * (docs/API_CONTRACT.md) con datos en memoria para que la UI se desarrolle sin
 * el server Go aditivo. No simula métricas (views/engagement siguen null).
 */

import type { IncomingMessage, ServerResponse } from 'node:http'
import type { Plugin } from 'vite'
import type {
  ClipListItem,
  Health,
  Job,
  JobAction,
  JobType,
  Paginated,
  ProcessedClip,
  Publication,
  PublicationListItem,
  Source,
  SourceClip,
  SourceClipStatus,
  SourceCreateInput,
  SourceDetail,
  SystemConfig,
  SystemOverview,
  VideoInfo,
  WorkerInfo,
} from './src/types/api'

interface Db {
  sources: Source[]
  sourceClips: SourceClip[]
  videos: Map<number, VideoInfo>
  clips: Map<number, ProcessedClip>
  publications: Publication[]
  jobs: Job[]
}

const now = (offsetMinutes = 0) => new Date(Date.now() - offsetMinutes * 60_000).toISOString()

const seedClip = (
  id: number,
  sourceId: number,
  platform: 'twitch' | 'kick',
  clipId: string,
  title: string,
  duration: number,
  minutesAgo: number,
  status: SourceClipStatus,
): SourceClip => ({
  id,
  platform,
  platform_clip_id: clipId,
  source_id: sourceId,
  title,
  duration_seconds: duration,
  created_at_platform: now(minutesAgo),
  status,
  error_message: null,
  created_at: now(minutesAgo),
  updated_at: now(minutesAgo),
})

const db: Db = {
  sources: [
    {
      id: 1,
      platform: 'twitch',
      channel_id: 'illojuan',
      channel_name: 'illojuan',
      active: true,
      last_checked_at: now(12),
      created_at: now(60 * 24 * 30),
      updated_at: now(12),
    },
    {
      id: 2,
      platform: 'kick',
      channel_id: 'riversgg',
      channel_name: 'Rivers_gg',
      active: true,
      last_checked_at: now(30),
      created_at: now(60 * 24 * 20),
      updated_at: now(30),
    },
  ],
  sourceClips: [
    seedClip(1, 1, 'twitch', 'ToxicJalapeñoGull-1', 'El rage masivo con el chat', 182, 40, 'detected'),
    seedClip(2, 1, 'twitch', 'AsianTacoDog-2', 'Reaccionando a mi peor clip', 90, 300, 'downloaded'),
    seedClip(3, 1, 'twitch', 'DeliciousPizzaPepe-3', 'Intentando el speedrun otra vez', 420, 600, 'downloaded'),
    seedClip(4, 2, 'kick', 'privatepeach3-4', 'Charla nocturna del jueves', 360, 1500, 'detected'),
    seedClip(5, 2, 'kick', 'marbled21915-5', 'Torneo annus final: resumen', 780, 2000, 'detected'),
    seedClip(6, 1, 'twitch', 'KappaWarOfWingers-6', 'Unboxing la nueva caja', 240, 2600, 'skipped'),
    seedClip(7, 2, 'kick', 'teachAPeregrine-7', 'Directo de 12h: montaje', 640, 3200, 'downloaded'),
    seedClip(8, 1, 'twitch', 'Mau5SpaceWood-8', 'El clip del año según chat', 148, 4000, 'detected'),
    seedClip(9, 2, 'kick', 'oxFireVip-9', 'Interacción con subs', 96, 4800, 'error'),
    seedClip(10, 1, 'twitch', 'uncleJimmy-10', 'Final de la ronda eliminatoria', 960, 5200, 'detected'),
  ],
  videos: new Map([
    [2, { id: 2, filepath: '/incoming/illojuan-2.mp4', status: 'completed', width: 1920, height: 1080, duration_seconds: 90 }],
    [3, { id: 3, filepath: '/incoming/illojuan-3.mp4', status: 'processing', width: null, height: null, duration_seconds: null }],
    [7, { id: 7, filepath: '/incoming/riversgg-7.mp4', status: 'completed', width: 1920, height: 1080, duration_seconds: 640 }],
  ]),
  clips: new Map([
    [2, { id: 2, filepath: '/completed/illojuan-2-vertical.mp4', thumbnail_path: '/thumbnails/illojuan-2.webp', status: 'completed', duration_sec: 90 }],
    [3, { id: 3, filepath: '/processing/illojuan-3-vertical.mp4', thumbnail_path: null, status: 'processing', duration_sec: null }],
    [7, { id: 7, filepath: '/completed/riversgg-7-vertical.mp4', thumbnail_path: '/thumbnails/riversgg-7.webp', status: 'completed', duration_sec: 640 }],
  ]),
  publications: [
    {
      id: 1,
      clip_id: 2,
      platform: 'youtube',
      status: 'published',
      attempts: 1,
      next_retry_at: null,
      external_id: '7341...',
      external_url: 'https://youtu.be/dQw4w9WgXcQ',
      error_message: null,
      published_at: now(120),
      created_at: now(200),
      updated_at: now(120),
    },
    {
      id: 2,
      clip_id: 2,
      platform: 'meta',
      status: 'pending',
      attempts: 0,
      next_retry_at: null,
      external_id: null,
      external_url: null,
      error_message: null,
      published_at: null,
      created_at: now(200),
      updated_at: now(200),
    },
    {
      id: 3,
      clip_id: 7,
      platform: 'meta',
      status: 'waiting_rate_limit',
      attempts: 2,
      next_retry_at: now(-30),
      external_id: null,
      external_url: null,
      error_message: 'rate limit exceeded',
      published_at: null,
      created_at: now(300),
      updated_at: now(30),
    },
  ],
  jobs: [
    { id: 3, type: 'publish', reference_id: 2, reference_type: 'clip', status: 'done', attempts: 1, locked_at: now(100), locked_by: 'worker-1', error_message: null, created_at: now(220), updated_at: now(110) },
    { id: 2, type: 'process', reference_id: 2, reference_type: 'clip', status: 'done', attempts: 1, locked_at: now(180), locked_by: 'worker-1', error_message: null, created_at: now(260), updated_at: now(150) },
    { id: 1, type: 'discovery', reference_id: 1, reference_type: 'source', status: 'running', attempts: 1, locked_at: now(5), locked_by: 'worker-1', error_message: null, created_at: now(160), updated_at: now(5) },
    { id: 4, type: 'poll_publications', reference_id: 3, reference_type: 'publication', status: 'queued', attempts: 0, locked_at: null, locked_by: null, error_message: null, created_at: now(2), updated_at: now(2) },
  ],
}

let nextSourceId = 10
let nextClipId = 100
let nextJobId = 100

function source(id: number): Source | undefined {
  return db.sources.find((s) => s.id === id)
}
function sourceClips(): SourceClip[] {
  return [...db.sourceClips].sort((a, b) => b.id - a.id)
}
function jobs(): Job[] {
  return [...db.jobs].sort((a, b) => b.id - a.id)
}

function clipItem(sc: SourceClip): ClipListItem {
  const src = source(sc.source_id)
  return {
    source_clip: sc,
    video: sc.status === 'downloaded' ? (db.videos.get(sc.id) ?? null) : null,
    clip: db.clips.get(sc.id) ?? null,
    channel: {
      id: src?.id ?? 0,
      channel_name: src?.channel_name ?? 'desconocido',
      platform: sc.platform,
      channel_id: src?.channel_id ?? '—',
    },
    publications: db.publications.filter((p) => db.clips.get(sc.id)?.id === p.clip_id),
    metrics: { views: null, views_per_hour: null, growth: null, engagement: null, available: false },
  }
}

function paginate<T>(rows: T[], page = 1, pageSize = 20): Paginated<T> {
  const safePage = Math.max(1, page)
  const safeSize = Math.max(1, pageSize)
  const start = (safePage - 1) * safeSize
  const slice = rows.slice(start, start + safeSize)
  const total = rows.length
  const pageCount = Math.ceil(total / safeSize)
  return {
    data: slice,
    pagination: {
      page: safePage,
      pageSize: safeSize,
      total,
      pageCount,
      hasNext: safePage < pageCount,
      hasPrev: safePage > 1,
    },
  }
}

function overview(): SystemOverview {
  const statusCount = <T extends string>(rows: { platform: string; status: T }[]): Record<string, Record<T, number>> =>
    rows.reduce<Record<string, Record<T, number>>>((acc, row) => {
      const by = (acc[row.platform] ??= {} as Record<T, number>)
      by[row.status] = (by[row.status] ?? 0) + 1
      return acc
    }, {})

  return {
    sources: db.sources.reduce<SystemOverview['sources']>((acc, s) => {
      const by = (acc[s.platform] ??= { active: 0, inactive: 0 })
      if (s.active) by.active += 1
      else by.inactive += 1
      return acc
    }, {}),
    source_clips: statusCount(sourceClips()),
    videos: {
      incoming: [...db.videos.values()].filter((v) => v.status === 'incoming').length,
      processing: [...db.videos.values()].filter((v) => v.status === 'processing').length,
      completed: [...db.videos.values()].filter((v) => v.status === 'completed').length,
      failed: [...db.videos.values()].filter((v) => v.status === 'failed').length,
    },
    clips: {
      processing: [...db.clips.values()].filter((c) => c.status === 'processing').length,
      completed: [...db.clips.values()].filter((c) => c.status === 'completed').length,
      failed: [...db.clips.values()].filter((c) => c.status === 'failed').length,
    },
    publications: statusCount(db.publications),
    jobs: jobs().reduce<SystemOverview['jobs']>((acc, j) => {
      const by = (acc[j.type] ??= {} as Record<Job['status'], number>)
      by[j.status] = (by[j.status] ?? 0) + 1
      return acc
    }, {}),
  }
}

function health(): Health {
  return { status: 'ok', schema_version: 2, worker: 'running' }
}

const config: SystemConfig = {
  poll_interval_seconds: 90,
  concurrency: 1,
  download_provider: 'yt-dlp',
  max_disk_usage_gb: 100,
  paths: { incoming: '/data/incoming', completed: '/data/completed', thumbnails: '/data/thumbnails' },
  credentials: { twitch: true, youtube: false, meta: false },
}

function workers(): WorkerInfo[] {
  return [...new Map(jobs().filter((j) => j.locked_by && j.status === 'running').map((j) => [j.locked_by!, j])).values()].map(
    (j) => ({ lockedBy: j.locked_by as string, jobType: j.type, lastLockedAt: j.locked_at }),
  )
}

function addJob(type: JobType, referenceId: number): Job {
  const job: Job = {
    id: nextJobId++,
    type,
    reference_id: referenceId,
    reference_type: type === 'download' ? 'source_clip' : 'source',
    status: 'queued',
    attempts: 0,
    locked_at: null,
    locked_by: null,
    error_message: null,
    created_at: now(0),
    updated_at: now(0),
  }
  db.jobs.push(job)
  return job
}

/* ──────────────────────────── HTTP plumbing ──────────────────────────── */

function send(res: ServerResponse, status: number, payload: unknown): void {
  res.statusCode = status
  res.setHeader('Content-Type', 'application/json')
  res.end(JSON.stringify(payload))
}

/** Envelope de error anidado real del backend: { error: { code, message, details } }. */
function errorBody(code: string, message: string): { error: { code: string; message: string; details: null } } {
  return { error: { code, message, details: null } }
}

/** Respuesta de una acción que encola un job (jobActionDTO). */
function jobAction(job: Job): JobAction {
  return { job, created: true }
}

function readBody(req: IncomingMessage): Promise<string> {
  return new Promise((resolve) => {
    let body = ''
    req.on('data', (chunk: Buffer) => {
      body += chunk.toString('utf-8')
    })
    req.on('end', () => resolve(body))
  })
}

type Handler = (segments: string[], query: URLSearchParams, req: IncomingMessage) => Promise<{ status: number; body: unknown }> | { status: number; body: unknown }

/* ─────────────────────────────── Routes ──────────────────────────────── */

const routes: Array<{ method: string; path: RegExp; handler: Handler }> = [
  {
    method: 'GET',
    path: /^\/api\/health$/,
    handler: () => ({ status: 200, body: health() }),
  },
  {
    method: 'GET',
    path: /^\/api\/system\/overview$/,
    handler: () => ({ status: 200, body: overview() }),
  },
  {
    method: 'GET',
    path: /^\/api\/system\/config$/,
    handler: () => ({ status: 200, body: config }),
  },
  {
    method: 'GET',
    path: /^\/api\/workers$/,
    handler: () => ({ status: 200, body: { data: workers() } }),
  },
  {
    method: 'GET',
    path: /^\/api\/jobs\/stats$/,
    handler: () => ({ status: 200, body: overview().jobs }),
  },
  {
    method: 'GET',
    path: /^\/api\/jobs$/,
    handler: (_seg, query) => {
      const { page, pageSize } = parsePaging(query)
      const rows = jobs()
      return { status: 200, body: paginate(rows, page, pageSize) }
    },
  },
  {
    method: 'GET',
    path: /^\/api\/sources$/,
    handler: (_seg, query) => {
      const platform = query.get('platform')
      const active = query.get('active')
      let rows = db.sources
      if (platform) rows = rows.filter((s) => s.platform === platform)
      if (active === 'true') rows = rows.filter((s) => s.active)
      return { status: 200, body: paginate(rows) }
    },
  },
  {
    method: 'POST',
    path: /^\/api\/sources$/,
    handler: async (_seg, _query, req) => {
      const input = JSON.parse(await readBody(req)) as SourceCreateInput
      const nowIso = now(0)
      const created: Source = {
        id: nextSourceId++,
        platform: input.platform,
        channel_id: input.channel_id,
        channel_name: input.channel_name,
        active: input.active,
        last_checked_at: null,
        created_at: nowIso,
        updated_at: nowIso,
      }
      db.sources.push(created)
      return { status: 201, body: created }
    },
  },
  {
    method: 'GET',
    path: /^\/api\/sources\/(\d+)$/,
    handler: ([id]) => {
      const s = source(Number(id))
      if (!s) return { status: 404, body: errorBody('NOT_FOUND', `source ${id} not found`) }
      const related = db.sourceClips.filter((sc) => sc.source_id === s.id)
      const detail: SourceDetail = {
        ...s,
        counts: {
          clipsDetected: related.length,
          clipsDownloaded: related.filter((sc) => sc.status === 'downloaded').length,
          clipsCompleted: related.filter((sc) => db.clips.get(sc.id)?.status === 'completed').length,
          clipsFailed: related.filter((sc) => sc.status === 'error').length,
        },
      }
      return { status: 200, body: detail }
    },
  },
  {
    method: 'PATCH',
    path: /^\/api\/sources\/(\d+)$/,
    handler: async ([id], _query, req) => {
      const s = source(Number(id))
      if (!s) return { status: 404, body: errorBody('NOT_FOUND', `source ${id} not found`) }
      const patch = JSON.parse(await readBody(req)) as Partial<SourceCreateInput>
      const updated = { ...s, ...patch, updated_at: now(0) }
      db.sources[db.sources.indexOf(s)] = updated
      return { status: 200, body: updated }
    },
  },
  {
    method: 'DELETE',
    path: /^\/api\/sources\/(\d+)$/,
    handler: ([id]) => {
      const s = source(Number(id))
      if (!s) return { status: 404, body: errorBody('NOT_FOUND', `source ${id} not found`) }
      db.sources = db.sources.filter((x) => x.id !== s.id)
      return { status: 204, body: null }
    },
  },
  {
    method: 'POST',
    path: /^\/api\/sources\/(\d+)\/discovery$/,
    handler: ([id]) => {
      const s = source(Number(id))
      if (!s) return { status: 404, body: errorBody('NOT_FOUND', `source ${id} not found`) }
      s.last_checked_at = now(0)
      db.sourceClips.push(seedClip(nextClipId++, s.id, s.platform, `new-clip-${nextClipId - 1}`, `Nuevo clip de ${s.channel_name}`, 120, 0, 'detected'))
      return { status: 200, body: jobAction(addJob('discovery', s.id)) }
    },
  },
  {
    method: 'GET',
    path: /^\/api\/sources\/(\d+)\/clips$/,
    handler: ([id], query) => {
      const { page, pageSize } = parsePaging(query)
      const rows = sourceClips().filter((sc) => sc.source_id === Number(id))
      return { status: 200, body: paginate(rows, page, pageSize) }
    },
  },
  {
    method: 'GET',
    path: /^\/api\/clips$/,
    handler: (_seg, query) => {
      const { page, pageSize } = parsePaging(query)
      let rows = sourceClips()
      const platform = query.get('platform')
      if (platform) rows = rows.filter((sc) => sc.platform === platform)
      const status = query.get('status')
      if (status) {
        const derived = status === 'processing' || status === 'completed' || status === 'failed'
        rows = rows.filter((sc) => (derived ? db.clips.get(sc.id)?.status === status : sc.status === status))
      }
      const channelId = query.get('channel_id')
      if (channelId) rows = rows.filter((sc) => sc.source_id === Number(channelId))
      const sort = query.get('sort') ?? 'newest'
      rows = [...rows].sort((a, b) => {
        if (sort === 'duration') return (b.duration_seconds - a.duration_seconds) || (b.created_at < a.created_at ? -1 : 1)
        return (b.created_at_platform ?? b.created_at) < (a.created_at_platform ?? a.created_at) ? -1 : 1
      })
      const list = rows.map(clipItem)
      return { status: 200, body: paginate(list, page, pageSize) }
    },
  },
  {
    method: 'GET',
    path: /^\/api\/clips\/(\d+)$/,
    handler: ([id]) => {
      const sc = db.sourceClips.find((x) => x.id === Number(id))
      if (!sc) return { status: 404, body: errorBody('NOT_FOUND', `clip ${id} not found`) }
      return { status: 200, body: clipItem(sc) }
    },
  },
  {
    method: 'GET',
    path: /^\/api\/source-clips\/(\d+)$/,
    handler: ([id]) => {
      const sc = db.sourceClips.find((x) => x.id === Number(id))
      if (!sc) return { status: 404, body: errorBody('NOT_FOUND', `source clip ${id} not found`) }
      return { status: 200, body: sc }
    },
  },
  {
    method: 'POST',
    path: /^\/api\/source-clips\/(\d+)\/download$/,
    handler: ([id]) => {
      const sc = db.sourceClips.find((x) => x.id === Number(id))
      if (!sc) return { status: 404, body: errorBody('NOT_FOUND', `source clip ${id} not found`) }
      if (sc.status !== 'detected') {
        return { status: 409, body: errorBody('CONFLICT', `el clip está en estado ${sc.status}`) }
      }
      sc.status = 'downloaded'
      sc.updated_at = now(0)
      db.videos.set(sc.id, { id: sc.id, filepath: `/incoming/${sc.platform}-${sc.id}.mp4`, status: 'processing', width: null, height: null, duration_seconds: null })
      db.clips.set(sc.id, { id: sc.id, filepath: `/processing/${sc.platform}-${sc.id}.mp4`, thumbnail_path: null, status: 'processing', duration_sec: null })
      return { status: 200, body: jobAction(addJob('download', sc.id)) }
    },
  },
  {
    method: 'POST',
    path: /^\/api\/source-clips\/(\d+)\/skip$/,
    handler: ([id]) => {
      const sc = db.sourceClips.find((x) => x.id === Number(id))
      if (!sc) return { status: 404, body: errorBody('NOT_FOUND', `source clip ${id} not found`) }
      sc.status = 'skipped'
      sc.updated_at = now(0)
      return { status: 200, body: sc }
    },
  },
  {
    method: 'GET',
    path: /^\/api\/publications$/,
    handler: (_seg, query) => {
      const { page, pageSize } = parsePaging(query)
      const platform = query.get('platform')
      const status = query.get('status')
      let rows = db.publications
      if (platform) rows = rows.filter((p) => p.platform === platform)
      if (status) rows = rows.filter((p) => p.status === status)
      const list: PublicationListItem[] = rows.map((p) => {
        const clipId = p.clip_id
        const sc = db.sourceClips.find((x) => db.clips.get(x.id)?.id === clipId)
        return {
          ...p,
          clip: { id: clipId, title: sc?.title ?? '—', platform: sc?.platform ?? 'twitch' },
        }
      })
      return { status: 200, body: paginate(list, page, pageSize) }
    },
  },
]

function parsePaging(query: URLSearchParams) {
  return {
    page: Number(query.get('page') ?? 1),
    pageSize: Number(query.get('pageSize') ?? 20),
  }
}

let routeCache: Array<{ method: string; regex: RegExp; handler: Handler }> | null = null

function match(reqMethod: string, pathname: string) {
  routeCache ??= routes.map((r) => ({ method: r.method, regex: r.path, handler: r.handler }))
  for (const route of routeCache) {
    if (route.method !== reqMethod) continue
    const m = route.regex.exec(pathname)
    if (m) {
      const segments = m.slice(1)
      return { handler: route.handler, segments }
    }
  }
  return null
}

export function devMockPlugin(): Plugin {
  return {
    name: 'clipfactory-dev-mock',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        void (async () => {
          if (!req.url || !req.url.startsWith('/api/')) {
            next()
            return
          }
          const url = new URL(req.url, 'http://localhost')
          const route = match(req.method ?? 'GET', url.pathname)
          if (!route) {
            send(res, 404, errorBody('NOT_FOUND', `DEV MOCK: ${req.method} ${url.pathname} no implementado`))
            return
          }

          try {
            const { status, body } = await route.handler(route.segments, url.searchParams, req)
            send(res, status, body)
          } catch (err) {
            const message = err instanceof Error ? err.message : 'mock error'
            send(res, 400, errorBody('VALIDATION_ERROR', `DEV MOCK: ${message}`))
          }
        })()
      })
    },
  }
}
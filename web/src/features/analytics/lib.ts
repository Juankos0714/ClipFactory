/**
 * Derivación de datos para Analytics desde la DB (API_CONTRACT.md §3.9).
 * El backend NO recolecta métricas de plataforma (🧭 BACKLOG): estos helpers
 * agregan SOLO lo que la DB ya tiene (estados, fechas, intentos) para que la
 * UI no invente views/engagement ni simule nada.
 */
import type { JobStatus, PublicationListItem, PublicationStatus, SystemOverview } from '@/types/api'

export const PUBLICATION_STATUSES: PublicationStatus[] = [
  'pending',
  'published',
  'error',
  'waiting_rate_limit',
  'failed',
]

export const JOB_STATUSES: JobStatus[] = ['queued', 'running', 'done', 'error']

/** Tonos alineados a los badges de estados del contrato (status.ts). */
export const PUB_STATUS_COLORS: Record<PublicationStatus, string> = {
  pending: '#38bdf8',
  published: '#34d399',
  error: '#f87171',
  waiting_rate_limit: '#a78bfa',
  failed: '#fbbf24',
}

export const JOB_STATUS_COLORS: Record<JobStatus, string> = {
  queued: '#38bdf8',
  running: '#fbbf24',
  done: '#34d399',
  error: '#f87171',
}

const DAY_MS = 86_400_000

function dayKey(d: Date): string {
  return d.toISOString().slice(0, 10)
}

/** Conteos de publicaciones pivoteados por plataforma (0 si la DB no reporta). */
export function publicationStack(overview: SystemOverview): Array<Record<string, number | string>> {
  return Object.entries(overview.publications).map(([platform, byStatus]) => {
    const row: Record<string, number | string> = { platform }
    for (const status of PUBLICATION_STATUSES) row[status] = byStatus[status] ?? 0
    return row
  })
}

/** Conteos de jobs pivoteados por tipo (mismo shape que GET /api/jobs/stats). */
export function jobsStack(overview: SystemOverview): Array<Record<string, number | string>> {
  return Object.entries(overview.jobs).map(([type, byStatus]) => {
    const row: Record<string, number | string> = { type }
    for (const status of JOB_STATUSES) row[status] = byStatus[status] ?? 0
    return row
  })
}

export interface DayPublished {
  day: string
  published: number
}

/**
 * Publicaciones publicadas por día (UTC), últimos `days` días. Fecha de hoy =
 * el fin del rango deja huecos en cero para no engañar al ojo con "sin datos".
 */
export function publishedPerDay(
  publications: PublicationListItem[],
  days = 30,
  now: Date = new Date(),
): DayPublished[] {
  const end = new Date(now.getTime())
  end.setUTCHours(0, 0, 0, 0)
  const start = end.getTime() - (days - 1) * DAY_MS

  const map = new Map<string, number>()
  for (let i = 0; i < days; i++) map.set(dayKey(new Date(start + i * DAY_MS)), 0)

  for (const pub of publications) {
    if (!pub.published_at) continue
    const d = new Date(pub.published_at)
    if (Number.isNaN(d.getTime())) continue
    const key = dayKey(d)
    if (map.has(key)) map.set(key, (map.get(key) ?? 0) + 1)
  }

  return [...map.entries()].map(([day, published]) => ({ day, published }))
}
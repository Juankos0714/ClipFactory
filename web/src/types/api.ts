/**
 * DTOs tipados del contrato REST (docs/API_CONTRACT.md).
 * Cada tipo mapea 1:1 un objeto del contract; los marcadores REQUIRED/BACKLOG
 * viven en el contrato, no aquí.
 */

/** Envoltorio estándar de error (siempre enviado por el backend aditivo). */
export interface ApiErrorEnvelope {
  error: {
    code: string
    message: string
    details?: unknown
  }
}

/* ───────────────────────────── 3.1 System & Health ───────────────────────── */

export type WorkerRunningState = 'running' | 'stopped' | 'starting' | 'stopping'

/** GET /api/health — aliveness + versión de schema + estado del worker. */
export interface Health {
  status: 'ok' | 'degraded'
  schema_version: number
  worker: WorkerRunningState
}

/** GET /api/system/overview — la forma HTTP del `clipfactory status`. */
export interface SystemOverview {
  sources: Record<string, { active: number; inactive: number }>
  source_clips: Record<string, Record<SourceClipStatus, number>>
  videos: Record<'incoming' | 'processing' | 'completed' | 'failed', number>
  clips: Record<'processing' | 'completed' | 'failed', number>
  publications: Record<string, Record<PublicationStatus, number>>
  jobs: Record<string, Record<JobStatus, number>>
}

/** GET /api/system/config — config no sensible (cred. presentes/ausentes). */
export interface SystemConfig {
  poll_interval_seconds: number
  concurrency: number
  download_provider: string
  max_disk_usage_gb: number
  paths: {
    incoming: string
    completed: string
    thumbnails: string
  }
  credentials: Record<string, boolean>
}

/** GET /api/workers — workers activos derivados de la DB (locked_by de jobs). */
export interface WorkerInfo {
  lockedBy: string
  jobType: string
  lastLockedAt: string | null
}

/** GET /api/workers — va envuelto en `{data}` (sin envelope de paginación). */
export interface WorkersResponse {
  data: WorkerInfo[]
}

/* ───────────────────────────── 3.2 Channels / Sources ───────────────────── */

export type SourcePlatform = 'twitch' | 'kick'

/** Fila de la tabla `sources`. */
export interface Source {
  id: number
  platform: SourcePlatform
  channel_id: string
  channel_name: string
  active: boolean
  last_checked_at: string | null
  created_at: string
  updated_at: string
}

/** GET /api/sources/:id — detalle con conteos derivados. */
export interface SourceDetail extends Source {
  counts: {
    clipsDetected: number
    clipsDownloaded: number
    clipsCompleted: number
    clipsFailed: number
  }
}

export interface SourceCreateInput {
  platform: SourcePlatform
  channel_id: string
  channel_name: string
  active: boolean
}

export interface SourceUpdateInput {
  channel_name?: string
  active?: boolean
  channel_id?: string
}

/* ───────────────────────────── 3.3 Source clips ─────────────────────────── */

/** Estados REALES de la tabla `source_clips` (schema: detected/downloaded/skipped/error). */
export type SourceClipStatus = 'detected' | 'downloaded' | 'skipped' | 'error'

/**
 * Filtro `status` de GET /api/clips: además de los estados reales de source_clips
 * acepta los estados derivados del clip procesado (processing/completed/failed).
 */
export type ClipStatusFilter = SourceClipStatus | 'processing' | 'completed' | 'failed'

/** Fila de la tabla `source_clips`. */
export interface SourceClip {
  id: number
  platform: SourcePlatform
  platform_clip_id: string
  source_id: number
  title: string
  duration_seconds: number
  created_at_platform: string | null
  status: SourceClipStatus
  error_message: string | null
  created_at: string
  updated_at: string
}

/* ──────────────────────── 3.4 Clips — listado unificado ─────────────────── */

export interface VideoInfo {
  id: number
  filepath: string
  status: 'incoming' | 'processing' | 'completed' | 'failed'
  width: number | null
  height: number | null
  duration_seconds: number | null
}

export interface ProcessedClip {
  id: number
  filepath: string
  thumbnail_path: string | null
  status: 'processing' | 'completed' | 'failed'
  duration_sec: number | null
}

export type PublicationStatus =
  | 'pending'
  | 'published'
  | 'error'
  | 'waiting_rate_limit'
  | 'failed'

/** Plataformas de publicación válidas (el backend rechaza el resto). */
export type PublicationPlatform = 'youtube' | 'meta'

export interface Publication {
  id: number
  clip_id: number
  platform: PublicationPlatform
  status: PublicationStatus
  attempts: number
  next_retry_at: string | null
  external_id: string | null
  external_url: string | null
  error_message: string | null
  published_at: string | null
  created_at: string
  updated_at: string
}

/** Métricas de plataforma. null hasta que el backend las recolecte (BACKLOG). */
export interface ClipMetrics {
  views: number | null
  views_per_hour: number | null
  growth: number | null
  engagement: number | null
  available: boolean
}

export interface ClipChannelRef {
  id: number
  channel_name: string
  platform: SourcePlatform
  channel_id: string
}

/** DTO agregado que el backend compone por JOIN (una fila por clip detectado). */
export interface ClipListItem {
  source_clip: SourceClip
  video: VideoInfo | null
  clip: ProcessedClip | null
  channel: ClipChannelRef
  publications: Publication[]
  metrics: ClipMetrics
}

/** GET /api/clips/:id — árbol completo de un clip detectado. */
export type ClipDetail = ClipListItem

/* ───────────────────────────── 3.7 Publications ─────────────────────────── */

export interface PublicationListItem extends Publication {
  clip: {
    id: number
    title: string
    platform: SourcePlatform
  }
}

/* ───────────────────────────── 3.8 Jobs / Cola ──────────────────────────── */

export type JobType =
  | 'discovery'
  | 'download'
  | 'process'
  | 'thumbnail'
  | 'publish'
  | 'poll_publications'

export type JobStatus = 'queued' | 'running' | 'done' | 'error'

/** Fila de la tabla `jobs`. */
export interface Job {
  id: number
  type: JobType
  reference_id: number
  reference_type: string
  status: JobStatus
  attempts: number
  locked_at: string | null
  locked_by: string | null
  error_message: string | null
  created_at: string
  updated_at: string
}

/** Respuesta de una acción que encola un job (jobActionDTO). */
export interface JobAction {
  job: Job
  created: boolean
}

/* ─────────────────────────────── 3.12 Logs ──────────────────────────────── */

/** Fila de la tabla `logs`. */
export interface Log {
  id: number
  level: string
  module: string
  message: string
  video_id: number | null
  clip_id: number | null
  job_id: number | null
  created_at: string
}

/* ─────────────────────────── Paginación (contrato §2.2) ─────────────────── */

export interface PaginationMeta {
  page: number
  pageSize: number
  total: number
  pageCount: number
  hasNext: boolean
  hasPrev: boolean
}

export interface Paginated<T> {
  data: T[]
  pagination: PaginationMeta
}

/** Cliente-lista de clips: admite los filtros del contrato §3.4. */
export interface ClipListParams {
  page?: number
  pageSize?: number
  channel_id?: number
  platform?: SourcePlatform
  status?: ClipStatusFilter
  from?: string
  to?: string
  sort?: 'newest' | 'duration'
  order?: 'asc' | 'desc'
}
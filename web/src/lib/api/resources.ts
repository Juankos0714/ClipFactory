/**
 * Capa de acceso a la API del contrato (docs/API_CONTRACT.md).
 * ÚNICO lugar con axios (AGENTS.md §4.2): cada función tipa request/response
 * contra el contrato. Los errores ya son DomainError (interceptor de client.ts).
 */

import { apiClient } from './client'
import { apiAssetUrl } from '@/lib/config/env'
import type {
  ClipDetail,
  ClipListParams,
  ClipListItem,
  Health,
  Job,
  JobAction,
  Log,
  Paginated,
  Publication,
  PublicationListItem,
  PublicationPlatform,
  PublicationStatus,
  Source,
  SourceClip,
  SourceCreateInput,
  SourceDetail,
  SourcePlatform,
  SourceUpdateInput,
  SystemConfig,
  SystemOverview,
  WorkersResponse,
} from '@/types/api'

/* ────────────────────────── 3.1 System & Health ─────────────────────────── */

export const systemApi = {
  health: () => apiClient.get<Health>('/api/health').then((r) => r.data),
  overview: () => apiClient.get<SystemOverview>('/api/system/overview').then((r) => r.data),
  config: () => apiClient.get<SystemConfig>('/api/system/config').then((r) => r.data),
  workers: () => apiClient.get<WorkersResponse>('/api/workers').then((r) => r.data.data),
}

/* ───────────────────────────── 3.2 Sources ──────────────────────────────── */

export interface SourceListParams {
  platform?: SourcePlatform
  active?: boolean
}

export const sourcesApi = {
  list: (params?: SourceListParams) =>
    apiClient.get<Paginated<Source>>('/api/sources', { params }).then((r) => r.data),
  get: (id: number) => apiClient.get<SourceDetail>(`/api/sources/${id}`).then((r) => r.data),
  create: (input: SourceCreateInput) =>
    apiClient.post<Source>('/api/sources', input).then((r) => r.data),
  patch: (id: number, input: SourceUpdateInput) =>
    apiClient.patch<Source>(`/api/sources/${id}`, input).then((r) => r.data),
  remove: (id: number) => apiClient.delete<void>(`/api/sources/${id}`).then(() => undefined),
  discover: (id: number) =>
    apiClient.post<JobAction>(`/api/sources/${id}/discovery`).then((r) => r.data),
  sourceClips: (id: number, params?: ClipListParams) =>
    apiClient.get<Paginated<ClipListItem>>(`/api/sources/${id}/clips`, { params }).then((r) => r.data),
}

/* ───────────────────────────── 3.3–3.5 Clips ────────────────────────────── */

export const clipsApi = {
  list: (params?: ClipListParams) =>
    apiClient.get<Paginated<ClipListItem>>('/api/clips', { params }).then((r) => r.data),
  detail: (id: number) => apiClient.get<ClipDetail>(`/api/clips/${id}`).then((r) => r.data),
  sourceClip: (id: number) => apiClient.get<SourceClip>(`/api/source-clips/${id}`).then((r) => r.data),
  download: (sourceClipId: number) =>
    apiClient.post<JobAction>(`/api/source-clips/${sourceClipId}/download`).then((r) => r.data),
  skip: (sourceClipId: number) =>
    apiClient.post<SourceClip>(`/api/source-clips/${sourceClipId}/skip`).then((r) => r.data),
  queueForDownload: (clipId: number) =>
    apiClient.post<JobAction>(`/api/clips/${clipId}/queue-for-download`).then((r) => r.data),
  queueForProcess: (clipId: number) =>
    apiClient.post<JobAction>(`/api/clips/${clipId}/queue-for-process`).then((r) => r.data),
  regenerateThumbnail: (clipId: number) =>
    apiClient.post<JobAction>(`/api/clips/${clipId}/regenerate-thumbnail`).then((r) => r.data),
  videoUrl: (id: number) => apiAssetUrl(`/api/clips/${id}/video`),
  thumbnailUrl: (id: number) => apiAssetUrl(`/api/clips/${id}/thumbnail`),
  processedUrl: (id: number) => apiAssetUrl(`/api/clips/${id}/processed`),
}

/* ───────────────────────────── 3.7 Publications ─────────────────────────── */

export interface PublicationListParams {
  platform?: PublicationPlatform
  status?: PublicationStatus
  from?: string
  to?: string
  page?: number
  pageSize?: number
}

export interface PublicationCreateInput {
  clip_id: number
  platform: PublicationPlatform
}

export const publicationsApi = {
  list: (params?: PublicationListParams) =>
    apiClient.get<Paginated<PublicationListItem>>('/api/publications', { params }).then((r) => r.data),
  get: (id: number) =>
    apiClient.get<PublicationListItem>(`/api/publications/${id}`).then((r) => r.data),
  create: (input: PublicationCreateInput) =>
    apiClient.post<Publication>('/api/publications', input).then((r) => r.data),
  retry: (id: number) =>
    apiClient.post<JobAction>(`/api/publications/${id}/retry`).then((r) => r.data),
  cancel: (id: number) =>
    apiClient.post<PublicationListItem>(`/api/publications/${id}/cancel`).then((r) => r.data),
}

/* ───────────────────────────── 3.8 Jobs / Cola ──────────────────────────── */

export interface JobListParams {
  type?: string
  status?: string
  page?: number
  pageSize?: number
}

export const jobsApi = {
  list: (params?: JobListParams) =>
    apiClient.get<Paginated<Job>>('/api/jobs', { params }).then((r) => r.data),
  stats: () => apiClient.get<SystemOverview['jobs']>('/api/jobs/stats').then((r) => r.data),
  get: (id: number) => apiClient.get<Job>(`/api/jobs/${id}`).then((r) => r.data),
  retry: (id: number) => apiClient.post<JobAction>(`/api/jobs/${id}/retry`).then((r) => r.data),
  cancel: (id: number) => apiClient.post<Job>(`/api/jobs/${id}/cancel`).then((r) => r.data),
}

/* ─────────────────────────────── 3.12 Logs ──────────────────────────────── */

export interface LogListParams {
  level?: string
  module?: string
  page?: number
  pageSize?: number
}

export const logsApi = {
  list: (params?: LogListParams) =>
    apiClient.get<Paginated<Log>>('/api/logs', { params }).then((r) => r.data),
}

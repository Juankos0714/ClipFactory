import type { PublicationStatus, JobStatus, SourceClipStatus, SystemOverview } from '@/types/api'

/** Suma de canales activos (todas las plataformas). */
export function activeSources(overview: SystemOverview): number {
  return Object.values(overview.sources).reduce((sum, s) => sum + s.active, 0)
}

/** Alguno de los estados de la máquina de estados de source_clips. */
export function sourceClipsBy(overview: SystemOverview, status: SourceClipStatus): number {
  return Object.values(overview.source_clips).reduce((sum, byPlatform) => sum + (byPlatform[status] ?? 0), 0)
}

export function clipsBy(overview: SystemOverview, status: 'processing' | 'completed' | 'failed'): number {
  return overview.clips[status]
}

export function publicationsBy(overview: SystemOverview, status: PublicationStatus): number {
  return Object.values(overview.publications).reduce((sum, byPlatform) => sum + (byPlatform[status] ?? 0), 0)
}

export function jobsBy(overview: SystemOverview, status: JobStatus): number {
  return Object.values(overview.jobs).reduce((sum, byType) => sum + (byType[status] ?? 0), 0)
}
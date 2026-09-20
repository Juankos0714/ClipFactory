/** Reglas de acción sobre publicaciones (API_CONTRACT.md §3.7). */
import type { PublicationPlatform, PublicationStatus } from '@/types/api'

export const PUBLISHING_PLATFORM_LABELS: Record<PublicationPlatform, string> = {
  youtube: 'YouTube',
  meta: 'Meta',
}

export const PUBLICATION_STATUS_OPTIONS: Array<{ value: PublicationStatus; label: string }> = [
  { value: 'pending', label: 'Pendiente' },
  { value: 'published', label: 'Publicada' },
  { value: 'error', label: 'Error' },
  { value: 'waiting_rate_limit', label: 'Rate limit' },
  { value: 'failed', label: 'Fallida' },
]

/** Reintentar solo tiene sentido en `error` (el resto o ya se publicó o está en cola). */
export function canRetry(status: PublicationStatus): boolean {
  return status === 'error'
}

/** Cancelar = dead-letter (`failed`), solo antes de publicarse. */
export function canCancel(status: PublicationStatus): boolean {
  return status === 'pending' || status === 'error' || status === 'waiting_rate_limit'
}
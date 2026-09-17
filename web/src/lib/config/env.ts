/** Variables de entorno tipadas. Ninguna contiene secretos (AGENTS.md §7). */

/**
 * Base URL del backend aditivo Go (clipfactory server).
 * - Dev: vacío → proxy `/api` de Vite (o mock middleware).
 * - Prod: origin de la API, SIN el prefijo `/api` (el contrato ya lo incluye).
 */
export const API_BASE_URL: string = (import.meta.env.VITE_API_URL ?? '').replace(/\/+$/, '')

export const REQUEST_TIMEOUT = Number(import.meta.env.VITE_API_TIMEOUT ?? 15000)

/** Refetch por defecto entre vistas (polling controlado; no inventar realtime). */
export const REFRESH_DASHBOARD_MS = 10_000

/** URL absoluta de un recurso (thumbnails/streams). */
export function apiAssetUrl(path: string): string {
  return `${API_BASE_URL}${path}`
}
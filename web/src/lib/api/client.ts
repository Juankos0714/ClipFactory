import axios, { type InternalAxiosRequestConfig } from 'axios'
import { API_BASE_URL, REQUEST_TIMEOUT } from '../config/env'
import { toDomainError, type DomainError } from './errors'
import { getToken, notifyUnauthorized } from '../auth/session'

/**
 * apiClient — instancia axios compartida (AGENTS.md §7).
 * - Base URL: VITE_API_URL (prod) o proxy `/api` (dev).
 * - Todos los errores se normalizan a DomainError en el interceptor de respuesta.
 * - Auth aditiva: si hay token de operador (sesión), se manda como Bearer.
 * - Un 401 (AUTHENTICATION_REQUIRED del server aditivo) se difunde al provider
 *   para activar login/protected routes. NUNCA se inyectan secretos de VITE_*.
 */
export const apiClient = axios.create({
  baseURL: API_BASE_URL || '/',
  timeout: REQUEST_TIMEOUT,
  headers: { 'Content-Type': 'application/json' },
})

apiClient.interceptors.request.use((config: InternalAxiosRequestConfig) => {
  const token = getToken()
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

apiClient.interceptors.response.use(
  (response) => response,
  (error: unknown) => {
    const domain = toDomainError(error)
    if (domain.kind === 'unauthorized') notifyUnauthorized()
    return Promise.reject(domain)
  },
)

export type { DomainError }
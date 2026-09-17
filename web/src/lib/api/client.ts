import axios, { type InternalAxiosRequestConfig } from 'axios'
import { API_BASE_URL, REQUEST_TIMEOUT } from '../config/env'
import { toDomainError, type DomainError } from './errors'

/**
 * apiClient — instancia axios compartida (AGENTS.md §7).
 * - Base URL: VITE_API_URL (prod) o proxy `/api` (dev).
 * - Todos los errores se normalizan a DomainError en el interceptor de respuesta.
 * - NUNCA se inyectan secretos aquí (los VITE_* van al bundle, son públicos).
 */
export const apiClient = axios.create({
  baseURL: API_BASE_URL || '/',
  timeout: REQUEST_TIMEOUT,
  headers: { 'Content-Type': 'application/json' },
})

apiClient.interceptors.request.use((config: InternalAxiosRequestConfig) => {
  // Placeholder: aquí se inyectaría el token de sessão del server aditivo cuando
  // exista autenticación (FASE 6). Hoy el backend es local/trusted → sin header.
  return config
})

apiClient.interceptors.response.use(
  (response) => response,
  (error: unknown) => Promise.reject(toDomainError(error)),
)

export type { DomainError }

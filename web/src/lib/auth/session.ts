/**
 * Sesión de operador frente al server aditivo (API_CONTRACT.md §2.4).
 * El backend solo exige auth si `CLIPFACTORY_API_TOKEN` está seteado; entonces
 * todo salvo /api/health requiere `Authorization: Bearer <token>`.
 *
 * El token se guarda en sessionStorage (se limpia al cerrar la pestaña) y en
 * memoria. Nunca viaja en VITE_*.
 */
export const AUTH_STORAGE_KEY = 'clipfactory.auth.token.v1'

let currentToken: string | null = null

function readStored(): string | null {
  if (typeof window === 'undefined') return null
  try {
    return window.sessionStorage.getItem(AUTH_STORAGE_KEY)
  } catch {
    return null
  }
}

function writeStored(token: string | null): void {
  if (typeof window === 'undefined') return
  try {
    if (token) window.sessionStorage.setItem(AUTH_STORAGE_KEY, token)
    else window.sessionStorage.removeItem(AUTH_STORAGE_KEY)
  } catch {
    // Modo incógnito o cuota llena: la sesión sigue activa en memoria.
  }
}

currentToken = readStored()

export function getToken(): string | null {
  return currentToken
}

/** true si el navegador recuerda un token de operador. */
export function isSessionActive(): boolean {
  return currentToken != null
}

/** Guarda/limpia el token (memoria + sessionStorage). No valida contra el backend. */
export function setSessionToken(token: string | null): void {
  currentToken = token
  writeStored(token)
}

/**
 * Observador único de hitos de autenticación difundidos por el apiClient.
 * El backend exige auth y la sesión no es válida → 401 → se notifica al
 * AuthProvider para que active login/protected routes (activación condicional).
 */
let unauthorizedHandler: (() => void) | null = null

export function registerUnauthorizedHandler(fn: (() => void) | null): void {
  unauthorizedHandler = fn
}

export function notifyUnauthorized(): void {
  unauthorizedHandler?.()
}
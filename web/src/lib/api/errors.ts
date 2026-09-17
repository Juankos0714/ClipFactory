/**
 * Taxonomía de errores de dominio (AGENTS.md §7 + §3: módulo único).
 * Normaliza cualquier causa de red/HTTP a `DomainError` con mensaje humano.
 * El frontend NUNCA muestra `AxiosError: ERR_NETWORK` crudo.
 *
 * Marcadores del contrato (API_CONTRACT.md §error-contract):
 * - `SERVER_NOT_READY_A_HINT` → endpoint REQUIRED que el backend aditivo aún no genera.
 * - `NOT_FOUND_IS_MISSING`    → 404 == "endpoint REQUIRED no implementado" (nunca simular).
 */

export type ApiErrorKind =
  | 'network'
  | 'timeout'
  | 'unauthorized'
  | 'forbidden'
  | 'not_found'
  | 'validation'
  | 'conflict'
  | 'server'
  | 'missing_endpoint'
  | 'upstream_down'

export interface DomainError extends Error {
  readonly kind: ApiErrorKind
  readonly status: number
  readonly fieldErrors?: Record<string, string>
  readonly retryable: boolean
}

/** Hint para endpoints REQUIRED que el backend aditivo aún no implementa. */
export const SERVER_NOT_READY_A_HINT =
  'FRONTEND REQUIRES BACKEND ENDPOINT: el server aditivo (clipfactory server) aún no expone este endpoint. La UI lo muestra bloqueado a propósito; la lógica no se simula.'

/** 404 sobre recurso individual == endpoint REQUIRED ausente (nunca simular). */
export const NOT_FOUND_IS_MISSING =
  'FRONTEND REQUIRES BACKEND ENDPOINT: no se encontró el recurso. Si es un endpoint del contrato, aún no está implementado en el server aditivo.'

const MSG = {
  NETWORK: 'No se pudo conectar con el backend. Comprobá que `clipfactory server` esté corriendo.',
  TIMEOUT: 'El backend tardó demasiado en responder. Intentá de nuevo.',
  AUTH: 'Sesión expirada. Iniciá sesión de nuevo.',
  FORBIDDEN: 'No tenés permiso para esta acción.',
  VALIDATION: 'Los datos enviados no son válidos.',
  NOT_FOUND: 'No se encontró el recurso solicitado.',
  CONFLICT: 'La operación entra en conflicto con el estado actual del recurso.',
  SERVER: 'El backend no pudo completar la operación. Intentá de nuevo.',
} as const

interface MakeErrorInput {
  kind: ApiErrorKind
  message: string
  status: number
  retryable: boolean
  fieldErrors?: Record<string, string>
}

function makeDomainError({ kind, message, status, retryable, fieldErrors }: MakeErrorInput): DomainError {
  return Object.assign(new Error(message), {
    name: 'DomainError',
    kind,
    status,
    retryable,
    ...(fieldErrors ? { fieldErrors } : {}),
  })
}

export function isDomainError(e: unknown): e is DomainError {
  return (
    typeof e === 'object' &&
    e !== null &&
    'kind' in e &&
    'status' in e &&
    typeof (e as { message?: unknown }).message === 'string'
  )
}

/** Envelope de error real del backend: `{ error: { code, message, details } }`. */
interface RawErrorEnvelope {
  error?: {
    code?: string
    message?: string
    details?: unknown
  }
}

/** Forma mínima de un error axios (la `data` usa el envelope de API_CONTRACT). */
interface RawHttpError {
  isAxiosError?: boolean
  code?: string
  message?: string
  response?: {
    status?: number
    data?: RawErrorEnvelope
  }
}

/** `details` solo se usa como fieldErrors si es un objeto de strings (el backend manda null). */
function asFieldErrors(details: unknown): Record<string, string> | undefined {
  if (typeof details !== 'object' || details === null || Array.isArray(details)) return undefined
  const entries = Object.entries(details as Record<string, unknown>).filter(
    ([, value]) => typeof value === 'string',
  )
  return entries.length > 0 ? (Object.fromEntries(entries) as Record<string, string>) : undefined
}

/**
 * Normaliza cualquier causa a DomainError.
 * 404 sobre recurso individual = isMissingEndpoint (endpoint REQUIRED ausente) → nunca simular.
 */
export function toDomainError(cause: unknown): DomainError {
  if (isDomainError(cause)) return cause

  const raw = cause as RawHttpError | undefined
  const status = raw?.response?.status ?? 0
  const envelope = raw?.response?.data?.error
  const detail = envelope?.message ?? raw?.message ?? ''
  const fieldErrors = asFieldErrors(envelope?.details)

  if (status === 401)
    return makeDomainError({ kind: 'unauthorized', message: MSG.AUTH, status, retryable: false })
  if (status === 403)
    return makeDomainError({ kind: 'forbidden', message: MSG.FORBIDDEN, status, retryable: false })
  if (status === 404)
    return makeDomainError({ kind: 'not_found', message: detail || NOT_FOUND_IS_MISSING, status, retryable: false })
  if (status === 409)
    return makeDomainError({ kind: 'conflict', message: detail || MSG.CONFLICT, status, retryable: false })
  if (status === 400 || status === 422)
    return makeDomainError({ kind: 'validation', message: detail || MSG.VALIDATION, status, retryable: false, fieldErrors })

  if (raw?.code === 'ECONNABORTED')
    return makeDomainError({ kind: 'timeout', message: MSG.TIMEOUT, status: 0, retryable: true })
  if (!status)
    return makeDomainError({
      kind: 'network',
      message: raw?.code === 'ERR_NETWORK' ? MSG.NETWORK : detail || 'Error de red inesperado.',
      status: 0,
      retryable: true,
    })
  if (status >= 500)
    return makeDomainError({ kind: 'server', message: MSG.SERVER, status, retryable: true })

  return makeDomainError({ kind: 'server', message: detail || 'Ocurrió un error inesperado.', status, retryable: true })
}

/** true si el error es un 404 con hint de endpoint REQUIRED (aditivo ausente). */
export function isMissingEndpointError(err: unknown): boolean {
  return isDomainError(err) && err.status === 404 && /endpoint/i.test(err.message)
}

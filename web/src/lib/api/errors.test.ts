import { describe, it, expect } from 'vitest'
import { toDomainError, SERVER_NOT_READY_A_HINT, NOT_FOUND_IS_MISSING, isMissingEndpointError } from './errors'

describe('toDomainError (taxonomía AGENTS.md §7)', () => {
  it('tipifica errores de red sin status como network/retryable', () => {
    const axios = { message: 'Network Error', isAxiosError: true } as unknown
    const err = toDomainError(axios)
    expect(err.kind).toBe('network')
    expect(err.retryable).toBe(true)
    expect(err.status).toBe(0)
  })

  it('tipifica 404 como not_found y NO retryable (nunca reintentar un missing endpoint)', () => {
    const axios = {
      isAxiosError: true,
      message: 'Request failed',
      response: {
        status: 404,
        data: { error: { code: 'NOT_FOUND', message: 'Not Found', details: null } },
      },
    } as unknown
    const err = toDomainError(axios)
    expect(err.kind).toBe('not_found')
    expect(err.retryable).toBe(false)
  })

  it('tipifica 400 como validation/fieldErrors y NO retryable', () => {
    const axios = {
      isAxiosError: true,
      response: {
        status: 400,
        data: {
          error: { code: 'VALIDATION_ERROR', message: 'Bad', details: { email: 'inválido' } },
        },
      },
    } as unknown
    const err = toDomainError(axios)
    expect(err.kind).toBe('validation')
    expect(err.retryable).toBe(false)
    expect(err.fieldErrors).toEqual({ email: 'inválido' })
  })

  it('tipifica 409 como conflict y NO retryable (UNIQUE/estado)', () => {
    const axios = {
      isAxiosError: true,
      response: {
        status: 409,
        data: { error: { code: 'CONFLICT', message: 'ya existe', details: null } },
      },
    } as unknown
    const err = toDomainError(axios)
    expect(err.kind).toBe('conflict')
    expect(err.retryable).toBe(false)
    expect(err.message).toBe('ya existe')
  })

  it('tipifica 500 como server/retryable', () => {
    const axios = {
      isAxiosError: true,
      response: {
        status: 500,
        data: { error: { code: 'INTERNAL_ERROR', message: 'boom', details: null } },
      },
    } as unknown
    const err = toDomainError(axios)
    expect(err.kind).toBe('server')
    expect(err.retryable).toBe(true)
  })

  it('devuelve el DomainError tal cual si ya lo es (delegación, no re-normalizar)', () => {
    const err = toDomainError({ message: 'x', isAxiosError: true, response: { status: 400 } })
    expect(toDomainError(err)).toBe(err)
  })
})

describe('marcadores del contrato (API_CONTRACT.md §error-contract)', () => {
  it('SERVER_NOT_READY_A_HINT nombra el marker FRONTEND REQUIRES BACKEND ENDPOINT', () => {
    expect(SERVER_NOT_READY_A_HINT).toMatch(/FRONTEND REQUIRES BACKEND ENDPOINT/i)
  })

  it('NOT_FOUND_IS_MISSING es el marcador de 404 == endpoint ausente', () => {
    expect(NOT_FOUND_IS_MISSING).toMatch(/endpoint/i)
  })

  it('isMissingEndpointError detecta 404 con hint de endpoint REQUIRED', () => {
    const err = toDomainError({
      isAxiosError: true,
      message: 'x',
      response: { status: 404, data: { error: { code: 'NOT_FOUND', message: 'No endpoint', details: null } } },
    })
    expect(isMissingEndpointError(err)).toBe(true)
  })
})

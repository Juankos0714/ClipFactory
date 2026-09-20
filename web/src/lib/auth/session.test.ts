import { describe, expect, it } from 'vitest'
import { getToken, isSessionActive, notifyUnauthorized, registerUnauthorizedHandler, setSessionToken } from './session'

describe('session (memoria; node no tiene sessionStorage)', () => {
  it('set/get del token en memoria', () => {
    setSessionToken('abc-123')
    expect(getToken()).toBe('abc-123')
    expect(isSessionActive()).toBe(true)
    setSessionToken(null)
    expect(getToken()).toBeNull()
    expect(isSessionActive()).toBe(false)
  })

  it('notifyUnuthorized: llama al handler registrado o nada sin él', () => {
    let called = 0
    registerUnauthorizedHandler(() => {
      called++
    })
    notifyUnauthorized()
    expect(called).toBe(1)
    registerUnauthorizedHandler(null)
    notifyUnauthorized()
    expect(called).toBe(1)
  })
})
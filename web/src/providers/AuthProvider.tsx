import { createContext, use, useEffect, useMemo, useState, type ReactNode } from 'react'
import type { Health } from '@/types/api'
import { useQuery } from '@tanstack/react-query'
import { apiClient } from '@/lib/api/client'
import { isMissingEndpointError } from '@/lib/api/errors'
import { REFRESH_DASHBOARD_MS } from '@/lib/config/env'
import { isSessionActive, registerUnauthorizedHandler, setSessionToken } from '@/lib/auth/session'

interface AuthContextValue {
  /** null == sin sesión de operador (no confundir con "no autorizado"). */
  user: { name: string; role: 'operator' } | null
  /** Endpoints REQUIRED que el backend aditivo aún no expone (bloqueados a propósito). */
  missingEndpoints: string[]
  /** ¿El navegador tiene token de operador guardado? */
  sessionActive: boolean
  /** ¿El server exige auth? Se activa al recibir 401 (CLIPFACTORY_API_TOKEN seteado). */
  serverRequiresAuth: boolean
  /** Guarda el token y lo envía como Bearer en las próximas peticiones. */
  login: (token: string) => void
  /** Limpia la sesión (no invalida el token en el server). */
  logout: () => void
  refreshSystemStatus: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

/**
 * Provider de auth/estado del sistema (FASE 7, aditivo y condicional).
 * - GET /api/health es público y mide el estado del pipeline.
 * - Si el backend exige token (APIToken seteado), el apiClient recibe un 401 →
 *   `serverRequiresAuth` se activa → la UI pide login (ProtectedRoute).
 * - Mientras el server no exija token, todo el flujo funciona sin sesión.
 * - Si el backend aún no expone un endpoint REQUIRED (404), NO lo simula.
 */
export function AuthProvider({ children }: { children: ReactNode }) {
  const [missingEndpoints, setMissingEndpoints] = useState<string[]>([])
  const [sessionActive, setSessionActive] = useState(isSessionActive)
  const [serverRequiresAuth, setServerRequiresAuth] = useState(false)

  const { refetch, error } = useQuery({
    queryKey: ['health'],
    queryFn: async () => {
      const { data } = await apiClient.get<Health>('/api/health')
      return data
    },
    refetchInterval: REFRESH_DASHBOARD_MS,
    retry: false,
  })

  // Endpoint REQUIRED no implementado (404) → la UI lo muestra bloqueado.
  useEffect(() => {
    if (isMissingEndpointError(error)) {
      setMissingEndpoints((prev) => (prev.includes('/api/health') ? prev : [...prev, '/api/health']))
    }
  }, [error])

  // Un 401 = el server exige auth y el Bearer faltó/no fue válido.
  useEffect(() => {
    registerUnauthorizedHandler(() => setServerRequiresAuth(true))
    return () => registerUnauthorizedHandler(null)
  }, [])

  const value = useMemo<AuthContextValue>(
    () => ({
      user: sessionActive ? { name: 'Operador', role: 'operator' } : null,
      missingEndpoints,
      sessionActive,
      serverRequiresAuth,
      login: (token: string) => {
        setSessionToken(token)
        setSessionActive(true)
      },
      logout: () => {
        setSessionToken(null)
        setSessionActive(false)
      },
      refreshSystemStatus: () => refetch().then(() => undefined),
    }),
    [missingEndpoints, sessionActive, serverRequiresAuth, refetch],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const ctx = use(AuthContext)
  if (!ctx) throw new Error('useAuth debe usarse dentro de <AuthProvider>')
  return ctx
}
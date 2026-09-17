import { createContext, useEffect, use, useMemo, useState } from 'react'
import type { Health } from '@/types/api'
import { useQuery } from '@tanstack/react-query'
import { apiClient } from '@/lib/api/client'
import { isMissingEndpointError } from '@/lib/api/errors'
import { REFRESH_DASHBOARD_MS } from '@/lib/config/env'

// AuthContext existe para que AGENTS.md §6 (REQUIRED endpoints) pueda consultar
// `permissions` y bloquear acciones que el backend REQUIRED aún no autoriza.
// HOY el backend es local/trusted: no hay login. FASE 6 añadirá auth aditiva.
interface AuthContextValue {
  /** null == sin sesión (no confundir con "no autorizado"). */
  user: { name: string; role: 'operator' } | null
  /** Endpoints REQUIRED que el backend aditivo aún no expone (bloqueados a propósito). */
  missingEndpoints: string[]
  refreshSystemStatus: () => Promise<void>
}

const AuthContext = createContext<AuthContextValue | null>(null)

/**
 * Provider de auth/estado del sistema.
 * - Consume GET /api/health (REQUIRED) para conocer estado del pipeline.
 * - Si el backend aún no lo expone (404), NO lo simula: expone `missingEndpoints`
 *   para que la UI muestre el 5º estado (bloqueado) honestamente.
 */
export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [missingEndpoints, setMissingEndpoints] = useState<string[]>([])

  const { refetch, error } = useQuery({
    queryKey: ['health'],
    queryFn: async () => {
      const { data } = await apiClient.get<Health>('/api/health')
      return data
    },
    refetchInterval: REFRESH_DASHBOARD_MS,
    retry: false,
  })

  // Cuando la query falla con 404 (endpoint REQUIRED no implementado), lo
  // registramos para que la UI lo muestre bloqueado, sin simular respuesta.
  useEffect(() => {
    if (isMissingEndpointError(error)) {
      setMissingEndpoints((prev) => (prev.includes('/api/health') ? prev : [...prev, '/api/health']))
    }
  }, [error])

  const value = useMemo<AuthContextValue>(
    () => ({ user: null, missingEndpoints, refreshSystemStatus: () => refetch().then(() => undefined) }),
    [missingEndpoints, refetch],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const ctx = use(AuthContext)
  if (!ctx) throw new Error('useAuth debe usarse dentro de <AuthProvider>')
  return ctx
}

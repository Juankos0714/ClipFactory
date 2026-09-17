import { QueryClient } from '@tanstack/react-query'
import { isDomainError } from '@/lib/api/errors'

/**
 * QueryClient compartido. La política de errores es global:
 * - `retry` automático SOLO para errores retryable (red/timeout/server 5xx).
 *   Errores 4xx (validación, no-auth) NO se reintentan.
 * - `throwOnError` false: cada página decide cómo renderizar sus 5 estados.
 */
export const queryClient: QueryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: (failureCount, error) =>
        isDomainError(error) && error.retryable && failureCount < 2,
      refetchOnWindowFocus: true,
    },
    mutations: {
      retry: false,
    },
  },
})

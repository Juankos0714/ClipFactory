import type { ReactNode } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient } from '@/lib/query/queryClient'
import { ErrorBoundary } from '@/components/gateways/ErrorBoundary'

/** Proveedor raíz de infraestructura: Query (server-state) + límite de errores fatales. */
export function RootProviders({ children }: { children: ReactNode }) {
  return (
    <QueryClientProvider client={queryClient}>
      <ErrorBoundary>{children}</ErrorBoundary>
    </QueryClientProvider>
  )
}

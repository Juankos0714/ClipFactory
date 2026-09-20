import { Navigate, Outlet, useLocation } from 'react-router-dom'
import { useAuth } from '@/providers/AuthProvider'

/**
 * Guard activado solo si el server aditivo exige auth (CLIPFACTORY_API_TOKEN)
 * y no hay sesión. Sin token en el server (dev), la ruta es transparente.
 * Redirige a /login recordando de dónde venía para volver tras autenticar.
 */
export function ProtectedRoute() {
  const { sessionActive, serverRequiresAuth } = useAuth()
  const location = useLocation()

  if (serverRequiresAuth && !sessionActive) {
    const from = location.pathname + location.search
    return <Navigate to="/login" replace state={{ from }} />
  }
  return <Outlet />
}
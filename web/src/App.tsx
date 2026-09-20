import { lazy } from 'react'
import { Route, Routes } from 'react-router-dom'
import { AppShell } from '@/components/layout/app-shell'
import { ProtectedRoute } from '@/components/gateways/protected-route'
import { LoginPage } from '@/features/auth/login-page'
import { ComingSoon } from '@/features/placeholder/coming-soon'
import { NotFound } from '@/features/placeholder/not-found'

// FASE 8 — code splitting por ruta: cada página es un chunk propio
// (Recharts queda fuera del bundle principal y se carga solo en /analytics).
const DashboardPage = lazy(() => import('@/features/dashboard/dashboard-page').then((m) => ({ default: m.DashboardPage })))
const ChannelsPage = lazy(() => import('@/features/channels/channels-page').then((m) => ({ default: m.ChannelsPage })))
const ClipsPage = lazy(() => import('@/features/clips/clips-page').then((m) => ({ default: m.ClipsPage })))
const ClipDetailPage = lazy(() => import('@/features/clips/clip-detail-page').then((m) => ({ default: m.ClipDetailPage })))
const ProductionPage = lazy(() => import('@/features/production/production-page').then((m) => ({ default: m.ProductionPage })))
const QueuePage = lazy(() => import('@/features/production/queue-page').then((m) => ({ default: m.QueuePage })))
const AutomationPage = lazy(() => import('@/features/automation/automation-page').then((m) => ({ default: m.AutomationPage })))
const PublicationsPage = lazy(() => import('@/features/publishing/publications-page').then((m) => ({ default: m.PublicationsPage })))
const AnalyticsPage = lazy(() => import('@/features/analytics/analytics-page').then((m) => ({ default: m.AnalyticsPage })))

/** Sitemap: 8 secciones del maestro + rutas de detalle, login y 404. */
export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<LoginPage />} />
      <Route element={<ProtectedRoute />}>
        <Route element={<AppShell />}>
          <Route path="/" element={<DashboardPage />} />
          <Route path="/channels" element={<ChannelsPage />} />
          <Route path="/clips" element={<ClipsPage />} />
          <Route path="/clips/:clipId" element={<ClipDetailPage />} />
          <Route path="/production" element={<ProductionPage />} />
          <Route path="/production/queue" element={<QueuePage />} />
          <Route path="/automation" element={<AutomationPage />} />
          <Route path="/publications" element={<PublicationsPage />} />
          <Route path="/analytics" element={<AnalyticsPage />} />
          <Route path="/settings" element={<ComingSoon name="Settings" />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Route>
    </Routes>
  )
}
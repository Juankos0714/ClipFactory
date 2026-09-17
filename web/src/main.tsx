import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from '@/App'
import { AuthProvider } from '@/providers/AuthProvider'
import { RootProviders } from '@/providers/RootProviders'
import '@/index.css'

const rootElement = document.getElementById('root')

if (!rootElement) {
  throw new Error('No se encontró el nodo #root en index.html')
}

createRoot(rootElement).render(
  <StrictMode>
    <RootProviders>
      <AuthProvider>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </AuthProvider>
    </RootProviders>
  </StrictMode>,
)
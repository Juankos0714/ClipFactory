import { Component, type ErrorInfo, type ReactNode } from 'react'

interface Props {
  children: ReactNode
}

interface State {
  hasError: boolean
}

/**
 * Límite de errores fatales (AGENTS.md §5 §7: 5º estado, honesto, nunca simulado).
 * - Muestra un mensaje humano EN ESPAÑOL + botón recargar (a11y: role="alert").
 * - NO muestra el `Error` crudo ni console.log (prohibido en prod).
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false }

  static getDerivedStateFromError(): State {
    return { hasError: true }
  }

  componentDidCatch(_error: Error, _info: ErrorInfo): void {
    // Sin console.log (AGENTS.md §5). El reporte aditivo llega en FASE 8.
  }

  render(): ReactNode {
    if (this.state.hasError) {
      return (
        <main role="alert" className="flex min-h-svh items-center justify-center p-4">
          <div className="max-w-md text-center">
            <h1 className="text-lg font-semibold">Algo salió mal</h1>
            <p className="mt-2 text-sm text-neutral-400">
              Ocurrió un error inesperado. Recargá la página para continuar. Si persiste,
              revisá que el server aditivo (clipfactory server) esté corriendo.
            </p>
            <button
              type="button"
              className="mt-4 rounded-md bg-brand px-4 py-2 text-sm font-medium text-white"
              onClick={() => window.location.reload()}
            >
              Recargar
            </button>
          </div>
        </main>
      )
    }
    return this.props.children
  }
}

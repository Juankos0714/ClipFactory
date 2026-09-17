/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Base URL del backend aditivo Go (clipfactory server). Requerido en prod. */
  readonly VITE_API_URL?: string
  /** Timeout (ms) por defecto del api client. Opcional. */
  readonly VITE_API_TIMEOUT?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}

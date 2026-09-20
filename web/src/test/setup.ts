import { afterEach } from 'vitest'
import { cleanup } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'

// vitest corre sin `globals: true` → RTL no auto-limpa el DOM entre tests.
afterEach(() => cleanup())
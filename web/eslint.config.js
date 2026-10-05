import { fileURLToPath } from 'node:url'
import js from '@eslint/js'
import globals from 'globals'
import react from 'eslint-plugin-react'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'

const tsconfigRootDir = fileURLToPath(new URL('.', import.meta.url))

export default tseslint.config(
  { ignores: ['dist', 'coverage'] },
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      ...tseslint.configs.recommendedTypeChecked,
      react.configs.flat.recommended,
      reactHooks.configs['recommended-latest'],
      reactRefresh.configs.recommended,
    ],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
      parserOptions: {
        project: ['./tsconfig.json', './tsconfig.node.json', './tsconfig.e2e.json'],
        tsconfigRootDir,
      },
    },
    settings: {
      react: { version: 'detect' },
    },
    rules: {
      'react/react-in-jsx-scope': 'off',
      'react/prop-types': 'off',
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
      '@typescript-eslint/consistent-type-imports': 'error',
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      'no-console': ['warn', { allow: ['warn', 'error'] }],
    },
  },
  {
    files: ['vite.config.ts'],
    languageOptions: {
      globals: globals.node,
    },
  },
  {
    // `expect(spy.method).toHaveBeenCalledWith(...)` es idiomático en tests: la
    // regla unbound-method no aporta nada ahí (siempre se verifica un mock).
    files: ['**/*.test.ts', '**/*.test.tsx', 'e2e/**/*.ts'],
    rules: {
      '@typescript-eslint/unbound-method': 'off',
    },
  },
)
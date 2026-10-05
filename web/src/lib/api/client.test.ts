import { AxiosError, AxiosHeaders, type AxiosAdapter, type InternalAxiosRequestConfig } from 'axios';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { apiClient } from './client';
import type { DomainError } from './client';
import * as session from '../auth/session';

vi.mock('../config/env', () => ({
  API_BASE_URL: '/api',
  REQUEST_TIMEOUT: 5000,
}));

vi.mock('../auth/session', () => ({
  getToken: vi.fn(),
  notifyUnauthorized: vi.fn(),
}));

/** Adapter que captura el config final (ya con interceptors aplicados) y responde 200. */
function okAdapter(): { adapter: AxiosAdapter; captured: InternalAxiosRequestConfig[] } {
  const captured: InternalAxiosRequestConfig[] = [];
  const adapter: AxiosAdapter = (config) => {
    captured.push(config);
    return Promise.resolve({ data: { status: 'ok' }, status: 200, statusText: 'OK', headers: {}, config });
  };
  return { adapter, captured };
}

/** Adapter que falla con un error axios con el status del contrato. */
function failingAdapter(status: number, code?: string, details?: unknown): AxiosAdapter {
  return (config) => {
    return Promise.reject(new AxiosError(
      `request failed with status code ${status}`,
      code ?? 'ERR_BAD_REQUEST',
      config,
      undefined,
      {
        status,
        statusText: 'Error',
        headers: {},
        config,
        data: { error: { code: 'TEST_ERROR', message: 'boom', details: details ?? null } },
      },
    ));
  };
}

async function captureRejection(promise: Promise<unknown>): Promise<DomainError> {
  try {
    await promise;
  } catch (err) {
    return err as DomainError;
  }
  throw new Error('se esperaba un rejection');
}

describe('apiClient', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('creates axios instance with correct config', () => {
    expect(apiClient.defaults.baseURL).toBe('/api');
    expect(apiClient.defaults.timeout).toBe(5000);
    expect(apiClient.defaults.headers['Content-Type']).toBe('application/json');
  });

  it('adds Authorization header when token exists', async () => {
    vi.mocked(session.getToken).mockReturnValue('test-token-123');
    const { adapter, captured } = okAdapter();

    await apiClient.get('/api/health', { adapter });

    expect(captured[0]?.headers.Authorization).toBe('Bearer test-token-123');
  });

  it('does not add Authorization header when no token', async () => {
    vi.mocked(session.getToken).mockReturnValue(null);
    const { adapter, captured } = okAdapter();

    await apiClient.get('/api/health', { adapter });

    expect(captured[0]?.headers.Authorization).toBeUndefined();
  });

  it('a 401 se difunde para que la UI active el login', async () => {
    const err = await captureRejection(apiClient.get('/api/sources', { adapter: failingAdapter(401) }));

    expect(err.kind).toBe('unauthorized');
    expect(err.status).toBe(401);
    expect(err.retryable).toBe(false);
    expect(session.notifyUnauthorized).toHaveBeenCalledTimes(1);
  });

  it('un 404 conserva el mensaje del backend y no activa el login', async () => {
    const err = await captureRejection(apiClient.get('/api/sources/9', { adapter: failingAdapter(404) }));

    expect(err.kind).toBe('not_found');
    expect(err.message).toBe('boom');
    expect(session.notifyUnauthorized).not.toHaveBeenCalled();
  });

  it('un 409 se marca como conflicto no reintentable', async () => {
    const err = await captureRejection(apiClient.post('/api/source-clips/1/download', {}, { adapter: failingAdapter(409) }));

    expect(err.kind).toBe('conflict');
    expect(err.retryable).toBe(false);
  });

  it('un 500 se marca como error de servidor reintentable', async () => {
    const err = await captureRejection(apiClient.get('/api/system/overview', { adapter: failingAdapter(503) }));

    expect(err.kind).toBe('server');
    expect(err.retryable).toBe(true);
  });

  it('sin respuesta HTTP el error es de red, no un AxiosError crudo', async () => {
    const adapter: AxiosAdapter = (config) =>
      Promise.reject(new AxiosError('Network Error', 'ERR_NETWORK', config));

    const err = await captureRejection(apiClient.get('/api/health', { adapter }));

    expect(err.kind).toBe('network');
    expect(err.retryable).toBe(true);
    expect(err.message).not.toContain('AxiosError');
    expect(session.notifyUnauthorized).not.toHaveBeenCalled();
  });

  it('un 400 con details de campo expone fieldErrors', async () => {
    const adapter = failingAdapter(400, 'ERR_BAD_REQUEST', { channel_id: 'obligatorio' });

    const err = await captureRejection(apiClient.post('/api/sources', {}, { adapter }));

    expect(err.kind).toBe('validation');
    expect(err.fieldErrors).toEqual({ channel_id: 'obligatorio' });
  });

  it('las cabeceras por defecto se envían en cada request', async () => {
    const { adapter, captured } = okAdapter();

    await apiClient.post('/api/sources', { channel_id: '1' }, { adapter });

    const headers = new AxiosHeaders(captured[0]?.headers);
    expect(headers.get('Content-Type')).toBe('application/json');
  });
});
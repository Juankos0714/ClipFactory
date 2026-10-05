import { beforeEach, describe, expect, it, vi } from 'vitest';
import { clipsApi, jobsApi, publicationsApi, sourcesApi, systemApi } from './resources';
import { apiClient } from './client';

vi.mock('./client', () => ({
  apiClient: {
    get: vi.fn().mockResolvedValue({ data: {} }),
    post: vi.fn().mockResolvedValue({ data: {} }),
    patch: vi.fn().mockResolvedValue({ data: {} }),
    delete: vi.fn().mockResolvedValue({}),
  },
}));

describe('API resources', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('sourcesApi recorre los endpoints de sources con sus verbs', async () => {
    await sourcesApi.list();
    expect(apiClient.get).toHaveBeenCalledWith('/api/sources', { params: undefined });

    await sourcesApi.get(3);
    expect(apiClient.get).toHaveBeenCalledWith('/api/sources/3');

    await sourcesApi.create({ platform: 'twitch', channel_id: 'illojuan', channel_name: 'illojuan', active: true });
    expect(apiClient.post).toHaveBeenCalledWith('/api/sources', {
      platform: 'twitch',
      channel_id: 'illojuan',
      channel_name: 'illojuan',
      active: true,
    });

    await sourcesApi.patch(3, { active: false });
    expect(apiClient.patch).toHaveBeenCalledWith('/api/sources/3', { active: false });

    await sourcesApi.remove(3);
    expect(apiClient.delete).toHaveBeenCalledWith('/api/sources/3');

    await sourcesApi.discover(3);
    expect(apiClient.post).toHaveBeenCalledWith('/api/sources/3/discovery');
  });

  it('clipsApi manda los filtros como query params', async () => {
    await clipsApi.list({ page: 2, pageSize: 10, platform: 'kick', status: 'detected' });
    expect(apiClient.get).toHaveBeenCalledWith('/api/clips', {
      params: { page: 2, pageSize: 10, platform: 'kick', status: 'detected' },
    });
  });

  it('clipsApi.download usa el endpoint de source-clips', async () => {
    await clipsApi.download(42);
    expect(apiClient.post).toHaveBeenCalledWith('/api/source-clips/42/download');
  });

  it('clipsApi encola reproceso y miniatura', async () => {
    await clipsApi.queueForProcess(42);
    expect(apiClient.post).toHaveBeenCalledWith('/api/clips/42/queue-for-process');

    await clipsApi.regenerateThumbnail(42);
    expect(apiClient.post).toHaveBeenCalledWith('/api/clips/42/regenerate-thumbnail');
  });

  it('clipsApi resuelve las URLs de assets sin el prefijo /api duplicado', () => {
    expect(clipsApi.videoUrl(7)).toBe('/api/clips/7/video');
    expect(clipsApi.thumbnailUrl(7)).toBe('/api/clips/7/thumbnail');
    expect(clipsApi.processedUrl(7)).toBe('/api/clips/7/processed');
  });

  it('jobsApi expone listado, stats y acciones', async () => {
    await jobsApi.list({ status: 'error' });
    expect(apiClient.get).toHaveBeenCalledWith('/api/jobs', { params: { status: 'error' } });

    await jobsApi.stats();
    expect(apiClient.get).toHaveBeenCalledWith('/api/jobs/stats');

    await jobsApi.retry(5);
    expect(apiClient.post).toHaveBeenCalledWith('/api/jobs/5/retry');

    await jobsApi.cancel(5);
    expect(apiClient.post).toHaveBeenCalledWith('/api/jobs/5/cancel');
  });

  it('publicationsApi filtra y reintenta publicaciones', async () => {
    await publicationsApi.list({ platform: 'meta', status: 'error' });
    expect(apiClient.get).toHaveBeenCalledWith('/api/publications', {
      params: { platform: 'meta', status: 'error' },
    });

    await publicationsApi.retry(9);
    expect(apiClient.post).toHaveBeenCalledWith('/api/publications/9/retry');
  });

  it('systemApi consulta los endpoints de sistema', async () => {
    await systemApi.health();
    expect(apiClient.get).toHaveBeenCalledWith('/api/health');

    await systemApi.overview();
    expect(apiClient.get).toHaveBeenCalledWith('/api/system/overview');

    await systemApi.config();
    expect(apiClient.get).toHaveBeenCalledWith('/api/system/config');

    await systemApi.workers();
    expect(apiClient.get).toHaveBeenCalledWith('/api/workers');
  });
});
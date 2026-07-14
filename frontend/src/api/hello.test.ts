import { describe, expect, test, vi, beforeEach } from 'vitest';

vi.mock('./request', () => ({
  default: {
    get: vi.fn(),
    post: vi.fn(),
  },
}));

import service from './request';
import { sayHello } from './hello';

const mockedGet = vi.mocked(service.get);

beforeEach(() => {
  vi.clearAllMocks();
});

describe('sayHello', () => {
  test('returns msg from /api/hello', async () => {
    mockedGet.mockResolvedValueOnce({ msg: 'hello world' });

    const result = await sayHello();

    expect(result).toEqual({ msg: 'hello world' });
    expect(mockedGet).toHaveBeenCalledWith('/api/hello', { params: undefined });
  });

  test('forwards optional params', async () => {
    mockedGet.mockResolvedValueOnce({ msg: 'hi' });

    await sayHello({ name: 'darven' });

    expect(mockedGet).toHaveBeenCalledWith('/api/hello', { params: { name: 'darven' } });
  });

  test('propagates service errors', async () => {
    mockedGet.mockRejectedValueOnce(new Error('network down'));

    await expect(sayHello()).rejects.toThrow('network down');
  });
});

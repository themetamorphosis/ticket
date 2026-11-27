import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  ApiError,
  cancelOrder,
  createOrder,
  csrfToken,
  getAvailability,
  getOrder,
  getQuote,
  loadEvent,
  resetSessionCache,
  startCheckout,
} from './api';

function reply(status, body) {
  return Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: () => (body === undefined ? Promise.reject(new Error('no body')) : Promise.resolve(body)),
  });
}

let fetchMock;
beforeEach(() => {
  resetSessionCache();
  fetchMock = vi.fn();
  vi.stubGlobal('fetch', fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

describe('api', () => {
  it('reads the snapshot, availability, quote and order with GET', async () => {
    fetchMock.mockImplementation(() => reply(200, { ok: true }));
    await loadEvent();
    await getAvailability('live show');
    await getQuote('live', 3);
    await getOrder('AB12C-DE34F');
    expect(fetchMock.mock.calls.map((c) => c[0])).toEqual([
      '/event.json',
      '/api/v1/events/live%20show/availability',
      '/api/v1/events/live/quote?quantity=3',
      '/api/v1/orders/AB12C-DE34F',
    ]);
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ method: 'GET', credentials: 'same-origin', body: undefined });
  });

  it('sends writes with the cached CSRF token and idempotency key', async () => {
    fetchMock
      .mockImplementationOnce(() => reply(200, { csrf_token: 'tok' }))
      .mockImplementation(() => reply(201, { ref: 'R' }));
    await createOrder('key-1', { quantity: 1 });
    await cancelOrder('R');
    expect(await csrfToken()).toBe('tok');
    expect(fetchMock).toHaveBeenCalledTimes(3);
    const [, create] = fetchMock.mock.calls[1];
    expect(create.method).toBe('POST');
    expect(create.body).toBe('{"quantity":1}');
    expect(create.headers).toEqual({
      'Content-Type': 'application/json',
      'X-CSRF-Token': 'tok',
      'Idempotency-Key': 'key-1',
    });
    expect(fetchMock.mock.calls[2][0]).toBe('/api/v1/orders/R/cancel');
    expect(fetchMock.mock.calls[2][1].headers).toEqual({ 'X-CSRF-Token': 'tok' });
  });

  it('turns API errors into ApiError with the server code and details', async () => {
    fetchMock.mockImplementation(() => reply(409, { error: { code: 'sold_out', message: 'Gone', remaining: 0 } }));
    const err = await getAvailability('live').catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 409, code: 'sold_out', message: 'Gone', details: { remaining: 0 } });
  });

  it('handles error responses without a JSON body', async () => {
    fetchMock.mockImplementation(() => reply(502));
    const err = await getAvailability('live').catch((e) => e);
    expect(err).toMatchObject({ status: 502, code: 'unknown', message: 'Something went wrong. Please try again.' });
  });

  it('reports network failures distinctly', async () => {
    fetchMock.mockImplementation(() => Promise.reject(new TypeError('offline')));
    const err = await getAvailability('live').catch((e) => e);
    expect(err).toMatchObject({ status: 0, code: 'network' });
    expect(new ApiError(0, 'x', 'y').details).toEqual({});
  });
});

describe('startCheckout', () => {
  it('posts with the CSRF token and returns the provider page', async () => {
    resetSessionCache();
    const fetchMock = vi.fn()
      .mockImplementationOnce(() => reply(200, { csrf_token: 'tok' }))
      .mockImplementationOnce(() => reply(200, { redirect_url: 'https://pay.example/1' }));
    vi.stubGlobal('fetch', fetchMock);
    expect(await startCheckout('AB12C DE34F')).toBe('https://pay.example/1');
    expect(fetchMock.mock.calls[1][0]).toBe('/api/v1/orders/AB12C%20DE34F/checkout');
    expect(fetchMock.mock.calls[1][1]).toMatchObject({ method: 'POST', headers: { 'X-CSRF-Token': 'tok' } });
  });
});

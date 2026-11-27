import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api, csrfToken, request, resetSessionCache, write } from './api';

function reply(status, body) {
  return Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: () => (body === undefined ? Promise.reject(new Error('none')) : Promise.resolve(body)),
  });
}

let fetchMock;
beforeEach(() => {
  resetSessionCache();
  fetchMock = vi.fn((path) => reply(200, path === '/api/v1/session' ? { csrf_token: 'tok' } : { ok: true }));
  vi.stubGlobal('fetch', fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

describe('staff api', () => {
  it('maps every call to its path and method', async () => {
    const calls = [
      [api.me(), 'GET', '/api/v1/staff/me'],
      [api.login('a@b.c', 'pw'), 'POST', '/api/v1/staff/login', '{"email":"a@b.c","password":"pw"}'],
      [api.acceptInvite('t', 'pw'), 'POST', '/api/v1/staff/invite/accept', '{"token":"t","password":"pw"}'],
      [api.logout(), 'POST', '/api/v1/staff/logout'],
      [api.myGates('live show'), 'GET', '/api/v1/ops/events/live%20show/my-gates'],
      [api.admit({ code: 'X' }), 'POST', '/api/v1/admissions', '{"code":"X"}'],
      [api.summary('live'), 'GET', '/api/v1/ops/events/live/summary'],
      [api.searchOrders('a b'), 'GET', '/api/v1/ops/orders?q=a%20b'],
      [api.order('R'), 'GET', '/api/v1/ops/orders/R'],
      [api.resend('R'), 'POST', '/api/v1/ops/orders/R/resend'],
      [api.refund('R', 'why'), 'POST', '/api/v1/ops/orders/R/refund', '{"reason":"why"}'],
      [api.reissue('T', 'why'), 'POST', '/api/v1/ops/tickets/T/reissue', '{"reason":"why"}'],
      [api.voidTicket('T', 'why'), 'POST', '/api/v1/ops/tickets/T/void', '{"reason":"why"}'],
      [api.finance(), 'GET', '/api/v1/ops/finance'],
      [api.event('live'), 'GET', '/api/v1/ops/events/live'],
      [api.updateEvent('live', { title: 'X' }), 'POST', '/api/v1/ops/events/live', '{"title":"X"}'],
      [api.setCapacity('live', 9), 'POST', '/api/v1/ops/events/live/capacity', '{"capacity":9}'],
      [api.publish('live'), 'POST', '/api/v1/ops/events/live/publish'],
      [api.gates('live'), 'GET', '/api/v1/ops/events/live/gates'],
      [api.addGate('live', 'N'), 'POST', '/api/v1/ops/events/live/gates', '{"name":"N"}'],
      [api.assign('G', 'S'), 'POST', '/api/v1/ops/gates/G/assign', '{"staff_id":"S"}'],
      [api.staff(), 'GET', '/api/v1/ops/staff'],
      [api.invite('e', 'n', 'gate'), 'POST', '/api/v1/ops/staff/invite', '{"email":"e","name":"n","role":"gate"}'],
      [api.disable('S', 'why'), 'POST', '/api/v1/ops/staff/S/disable', '{"reason":"why"}'],
      [api.audit(), 'GET', '/api/v1/ops/audit'],
    ];
    await Promise.all(calls.map((c) => c[0]));
    for (const [, method, path, body] of calls) {
      const call = fetchMock.mock.calls.find((c) => c[0] === path && c[1].method === method);
      expect(call, path).toBeTruthy();
      expect(call[1].method).toBe(method);
      if (body) expect(call[1].body).toBe(body);
      if (method === 'POST') expect(call[1].headers['X-CSRF-Token']).toBe('tok');
    }
    expect(fetchMock.mock.calls.filter((c) => c[0] === '/api/v1/session')).toHaveLength(1);
  });

  it('reports API, empty-body and network errors', async () => {
    fetchMock.mockImplementationOnce(() => reply(403, { error: { code: 'forbidden', message: 'No', extra: 1 } }));
    await expect(request('/x')).rejects.toMatchObject({ status: 403, code: 'forbidden', message: 'No', details: { extra: 1 } });
    fetchMock.mockImplementationOnce(() => reply(500));
    await expect(request('/x')).rejects.toMatchObject({ code: 'unknown', message: 'Something went wrong. Please try again.' });
    fetchMock.mockImplementationOnce(() => Promise.reject(new TypeError('offline')));
    await expect(request('/x')).rejects.toBeInstanceOf(ApiError);
    expect(new ApiError(0, 'x', 'y').details).toEqual({});
  });

  it('does not cache a failed session request', async () => {
    fetchMock.mockImplementationOnce(() => reply(503));
    await expect(csrfToken()).rejects.toMatchObject({ status: 503 });
    expect(await csrfToken()).toBe('tok');
  });

  it('caches the CSRF token until reset', async () => {
    await csrfToken();
    await write('/y', { a: 1 });
    resetSessionCache();
    await csrfToken();
    expect(fetchMock.mock.calls.filter((c) => c[0] === '/api/v1/session')).toHaveLength(2);
  });
});

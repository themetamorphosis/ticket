import { vi } from 'vitest';

// A fresh set of mocked staff API calls for each test file.
export function apiMock(actual) {
  const names = ['me', 'login', 'acceptInvite', 'logout', 'myGates', 'admit', 'summary',
    'searchOrders', 'order', 'sellOffline', 'resend', 'refund', 'reissue', 'voidTicket', 'finance', 'event', 'updateEvent', 'setCapacity',
    'publish', 'gates', 'addGate', 'assign', 'staff', 'invite', 'disable', 'audit'];
  return { ...actual, resetSessionCache: vi.fn(), api: Object.fromEntries(names.map((n) => [n, vi.fn()])) };
}

export function me(role, extra = {}) {
  return { staff: { id: 's1', name: 'Staff One', email: 's@example.org', role, status: 'active' }, events: [{ slug: 'live', title: 'Kacheem Live', currency: 'BDT', price_minor: 150000 }], ...extra };
}

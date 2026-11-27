// Client for the staff API. The staff site and API share the VPN-only origin.

export class ApiError extends Error {
  constructor(status, code, message, details = {}) {
    super(message);
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

const GENERIC = 'Something went wrong. Please try again.';

export async function request(path, { method = 'GET', body, headers = {} } = {}) {
  let res;
  try {
    res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: body === undefined ? headers : { 'Content-Type': 'application/json', ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, 'network', 'No connection to the server.');
  }
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    const e = data?.error ?? {};
    throw new ApiError(res.status, e.code ?? 'unknown', e.message ?? GENERIC, e);
  }
  return data;
}

let csrf = null;

// Concurrent first writes share one session request; a failure is not cached.
export function csrfToken() {
  csrf ??= request('/api/v1/session')
    .then((res) => res.csrf_token)
    .catch((err) => {
      csrf = null;
      throw err;
    });
  return csrf;
}

// Sign-in renews the session token; the CSRF token stays with the session,
// but fetch it again to be safe.
export function resetSessionCache() {
  csrf = null;
}

export async function write(path, body) {
  const token = await csrfToken();
  return request(path, { method: 'POST', body, headers: { 'X-CSRF-Token': token } });
}

const enc = encodeURIComponent;

export const api = {
  me: () => request('/api/v1/staff/me'),
  login: (email, password) => write('/api/v1/staff/login', { email, password }),
  acceptInvite: (token, password) => write('/api/v1/staff/invite/accept', { token, password }),
  logout: () => write('/api/v1/staff/logout'),
  myGates: (slug) => request(`/api/v1/ops/events/${enc(slug)}/my-gates`),
  admit: (scan) => write('/api/v1/admissions', scan),
  summary: (slug) => request(`/api/v1/ops/events/${enc(slug)}/summary`),
  searchOrders: (q) => request(`/api/v1/ops/orders?q=${enc(q)}`),
  order: (ref) => request(`/api/v1/ops/orders/${enc(ref)}`),
  resend: (ref) => write(`/api/v1/ops/orders/${enc(ref)}/resend`),
  refund: (ref, reason) => write(`/api/v1/ops/orders/${enc(ref)}/refund`, { reason }),
  reissue: (id, reason) => write(`/api/v1/ops/tickets/${enc(id)}/reissue`, { reason }),
  voidTicket: (id, reason) => write(`/api/v1/ops/tickets/${enc(id)}/void`, { reason }),
  finance: () => request('/api/v1/ops/finance'),
  event: (slug) => request(`/api/v1/ops/events/${enc(slug)}`),
  updateEvent: (slug, details) => write(`/api/v1/ops/events/${enc(slug)}`, details),
  setCapacity: (slug, capacity) => write(`/api/v1/ops/events/${enc(slug)}/capacity`, { capacity }),
  publish: (slug) => write(`/api/v1/ops/events/${enc(slug)}/publish`),
  gates: (slug) => request(`/api/v1/ops/events/${enc(slug)}/gates`),
  addGate: (slug, name) => write(`/api/v1/ops/events/${enc(slug)}/gates`, { name }),
  assign: (gateID, staffID) => write(`/api/v1/ops/gates/${enc(gateID)}/assign`, { staff_id: staffID }),
  staff: () => request('/api/v1/ops/staff'),
  invite: (email, name, role) => write('/api/v1/ops/staff/invite', { email, name, role }),
  disable: (id, reason) => write(`/api/v1/ops/staff/${enc(id)}/disable`, { reason }),
  audit: () => request('/api/v1/ops/audit'),
};

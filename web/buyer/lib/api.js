// Thin client for the Go buyer API. Every call is same-origin: NGINX serves
// this static site and proxies /api/v1/* to Go, so session cookies just work.

export class ApiError extends Error {
  constructor(status, code, message, details = {}) {
    super(message);
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

const GENERIC = 'Something went wrong. Please try again.';
const OFFLINE = 'We could not reach the ticket service. Check your connection and try again.';

async function request(path, { method = 'GET', body, headers = {} } = {}) {
  let res;
  try {
    res = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers: body === undefined ? headers : { 'Content-Type': 'application/json', ...headers },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, 'network', OFFLINE);
  }
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    const e = data?.error ?? {};
    throw new ApiError(res.status, e.code ?? 'unknown', e.message ?? GENERIC, e);
  }
  return data;
}

const enc = encodeURIComponent;

// The published event snapshot, served by NGINX with a short cache lifetime.
export function loadEvent() {
  return request('/event.json');
}

export function getAvailability(slug) {
  return request(`/api/v1/events/${enc(slug)}/availability`);
}

export function getQuote(slug, quantity) {
  return request(`/api/v1/events/${enc(slug)}/quote?quantity=${enc(quantity)}`);
}

let csrf = null;

// The session endpoint creates the anonymous buyer session and returns the
// CSRF token bound to it. It is cached for the life of the page.
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

export function resetSessionCache() {
  csrf = null;
}

export async function createOrder(idempotencyKey, booking) {
  const token = await csrfToken();
  return request('/api/v1/orders', {
    method: 'POST',
    body: booking,
    headers: { 'X-CSRF-Token': token, 'Idempotency-Key': idempotencyKey },
  });
}

export function getOrder(ref) {
  return request(`/api/v1/orders/${enc(ref)}`);
}

// Opens (or resumes) the payment session and returns the provider page URL.
export async function startCheckout(ref) {
  const token = await csrfToken();
  const res = await request(`/api/v1/orders/${enc(ref)}/checkout`, { method: 'POST', headers: { 'X-CSRF-Token': token } });
  return res.redirect_url;
}

export async function cancelOrder(ref) {
  const token = await csrfToken();
  return request(`/api/v1/orders/${enc(ref)}/cancel`, { method: 'POST', headers: { 'X-CSRF-Token': token } });
}

// Trades the secret from an SMS pickup link for access in this browser session.
export async function exchangePickup(secret) {
  const token = await csrfToken();
  return request('/api/v1/pickup/exchange', { method: 'POST', body: { secret }, headers: { 'X-CSRF-Token': token } });
}

// Lists the orders this browser opened through pickup links.
export function listPickups() {
  return request('/api/v1/pickup');
}

export async function resendTickets(ref) {
  const token = await csrfToken();
  return request(`/api/v1/orders/${enc(ref)}/resend`, { method: 'POST', headers: { 'X-CSRF-Token': token } });
}

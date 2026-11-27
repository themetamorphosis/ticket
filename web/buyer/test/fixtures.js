export const snapshot = {
  revision: 1,
  published_at: '2027-01-01T00:00:00Z',
  event: {
    slug: 'live',
    title: 'Kacheem Live',
    organizer: 'Kacheem Comms',
    venue: { name: 'Test Hall', address: '1 Test Road, Dhaka' },
    description: 'An evening of music.',
    lineup: ['Artist One', 'Artist Two'],
    starts_at: '2027-02-14T13:30:00Z',
    doors_at: '2027-02-14T12:30:00Z',
    time_zone: 'Asia/Dhaka',
    currency: 'BDT',
    max_per_order: 4,
    min_age: 0,
    sale_opens_at: '2027-01-10T04:00:00Z',
    sale_closes_at: '2027-02-14T12:00:00Z',
    ticket_type: { name: 'General admission', price_minor: 150000, fee_minor: 5000 },
  },
};

export function quote(quantity) {
  return {
    quantity,
    unit_price_minor: 150000,
    unit_fee_minor: 5000,
    subtotal_minor: 150000 * quantity,
    fees_minor: 5000 * quantity,
    total_minor: 155000 * quantity,
    currency: 'BDT',
  };
}

export function order(overrides = {}) {
  const now = Date.now();
  return {
    ref: 'AB12C-DE34F',
    status: 'held',
    event: { slug: 'live', title: 'Kacheem Live', starts_at: '2027-02-14T13:30:00Z', time_zone: 'Asia/Dhaka' },
    quantity: 3,
    unit_price_minor: 150000,
    unit_fee_minor: 5000,
    total_minor: 465000,
    currency: 'BDT',
    purchaser: { name: 'Nusrat Jahan', email: 'nusrat@example.com', phone: '+8801711000000', age: 28 },
    created_at: new Date(now).toISOString(),
    expires_at: new Date(now + 10 * 60 * 1000).toISOString(),
    server_time: new Date(now).toISOString(),
    payment_provider: 'fake',
    payment_status: null,
    timeline: [{ at: new Date(now).toISOString(), kind: 'order_created' }],
    tickets: [],
    deliveries: {},
    ...overrides,
  };
}

// A promise the test resolves or rejects when it chooses.
export function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

export function tickets(n, ready = true) {
  return Array.from({ length: n }, (_, i) => ({
    id: `t${i + 1}`,
    number: i + 1,
    of: n,
    status: 'valid',
    ready,
    pdf_url: `/api/v1/tickets/t${i + 1}/pdf`,
  }));
}

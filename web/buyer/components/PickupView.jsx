'use client';

import Link from 'next/link';
import { useEffect, useState } from 'react';
import { exchangePickup, listPickups } from '../lib/api';
import { formatDateTime } from '../lib/format';
import { TicketList } from './TicketList';

// Reads and removes the secret from the URL fragment so it does not stay in
// the address bar or browser history once exchanged.
function takeSecret() {
  const secret = window.location.hash.slice(1);
  if (secret) {
    window.history.replaceState(null, '', window.location.pathname);
  }
  return secret;
}

/**
 * The page an SMS pickup link opens. The secret is exchanged once for access
 * in this browser; after that the page lists the tickets without it.
 */
export function PickupView({ getSecret = takeSecret, pollMs = 3000, maxPolls = 20 }) {
  const [orders, setOrders] = useState(null);
  const [error, setError] = useState(null);
  const [polls, setPolls] = useState(0);

  useEffect(() => {
    const secret = getSecret();
    (secret ? exchangePickup(secret) : listPickups())
      .then((res) => setOrders(res.orders))
      .catch((err) =>
        setError(err.code === 'pickup_invalid' ? 'This ticket link is not valid or has expired.' : err.message),
      );
  }, [getSecret]);

  const preparing = orders?.some((o) => o.tickets.some((t) => !t.ready));
  useEffect(() => {
    if (!preparing || polls >= maxPolls) return undefined;
    const id = setTimeout(() => {
      listPickups()
        .then((res) => setOrders(res.orders))
        .catch(() => {})
        .finally(() => setPolls((n) => n + 1));
    }, pollMs);
    return () => clearTimeout(id);
  }, [preparing, polls, maxPolls, pollMs]);

  return (
    <main className="page pickup" aria-busy={orders || error ? undefined : 'true'}>
      <h1>Your tickets</h1>
      {error && (
        <p role="alert">
          {error} If you booked the tickets, open your order page to have them sent again.{' '}
          <Link href="/">Back to the event</Link>
        </p>
      )}
      {!orders && !error && <p>Loading your tickets…</p>}
      {orders?.length === 0 && <p>Open the link from your text message to see your tickets here.</p>}
      {orders?.map((o) => (
        <section key={o.ref} aria-labelledby={`order-${o.ref}`}>
          <h2 id={`order-${o.ref}`}>{o.event.title}</h2>
          <p className="hint">
            {formatDateTime(o.event.starts_at, o.event.time_zone)} · Order {o.ref}
          </p>
          <TicketList tickets={o.tickets} />
        </section>
      ))}
      <p className="hint">Each ticket admits one person, once. Show its QR code at the entrance, on screen or printed.</p>
    </main>
  );
}

'use client';

import Link from 'next/link';
import { useEffect, useState } from 'react';
import { ApiError, cancelOrder, getOrder } from '../lib/api';
import { formatCountdown, formatDateTime, formatMoney, formatTime } from '../lib/format';

function refFromLocation() {
  return new URLSearchParams(window.location.search).get('ref');
}

/**
 * The order page. It reloads the order from the server, so refreshing keeps
 * the order, and counts down the hold on the server clock, not the device's.
 */
export function OrderView({ getRef = refFromLocation }) {
  const [order, setOrder] = useState(null);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    const ref = getRef();
    (ref ? getOrder(ref) : Promise.reject(new ApiError(0, 'missing_ref', 'This link is missing its order reference.')))
      .then(setOrder)
      .catch((err) => setError(err.code === 'order_not_found' ? 'We could not find this order in this browser.' : err.message));
  }, [getRef]);

  async function cancel() {
    setBusy(true);
    try {
      setOrder(await cancelOrder(order.ref));
    } catch (err) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  }

  if (!order) {
    return (
      <main className="page" aria-busy={error ? undefined : 'true'}>
        <h1>Your order</h1>
        {error ? (
          <p role="alert">
            {error} <Link href="/">Back to the event</Link>
          </p>
        ) : (
          <p>Loading your order…</p>
        )}
      </main>
    );
  }

  const tz = order.event.time_zone;
  const money = (minor) => formatMoney(minor, order.currency);
  return (
    <main className="page order">
      <h1>Your order {order.ref}</h1>
      <Status order={order} onExpire={() => setOrder((o) => ({ ...o, status: 'expired' }))} />
      {error && <p role="alert">{error}</p>}
      <dl className="summary">
        <dt>Event</dt>
        <dd>
          {order.event.title}, {formatDateTime(order.event.starts_at, tz)}
        </dd>
        <dt>Tickets</dt>
        <dd>
          {order.quantity} × {money(order.unit_price_minor + order.unit_fee_minor)}
        </dd>
        <dt>Total ({order.currency})</dt>
        <dd className="total">{money(order.total_minor)}</dd>
        <dt>Purchaser</dt>
        <dd>
          {order.purchaser.name}, {order.purchaser.email}, {order.purchaser.phone}
        </dd>
      </dl>
      {order.status === 'held' && (
        <button type="button" className="secondary" onClick={cancel} disabled={busy}>
          {busy ? 'Releasing…' : 'Release these tickets'}
        </button>
      )}
    </main>
  );
}

function Status({ order, onExpire }) {
  const [now, setNow] = useState(() => Date.now());
  // Offset between the server clock and this device, fixed when the order loaded.
  const [offset] = useState(() => Date.parse(order.server_time) - Date.now());
  const left = Date.parse(order.expires_at) - (now + offset);
  const held = order.status === 'held';

  useEffect(() => {
    if (!held) return undefined;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [held]);

  useEffect(() => {
    if (held && left <= 0) onExpire();
  }, [held, left, onExpire]);

  if (held) {
    return (
      <p className="status held" role="status">
        Your tickets are held for <strong>{formatCountdown(left)}</strong>, until{' '}
        {formatTime(order.expires_at, order.event.time_zone)}. Payment is not available yet.
      </p>
    );
  }
  const text = {
    expired: (
      <>
        This hold has expired and the tickets were released. <Link href="/">Book again</Link>
      </>
    ),
    cancelled: (
      <>
        You released these tickets. <Link href="/">Book again</Link>
      </>
    ),
    paid: <>Payment received. Your tickets are on their way.</>,
  }[order.status];
  return (
    <p className="status" role="status">
      {text}
    </p>
  );
}

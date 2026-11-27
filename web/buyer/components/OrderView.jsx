'use client';

import Link from 'next/link';
import { useEffect, useState } from 'react';
import { ApiError, cancelOrder, getOrder, resendTickets, startCheckout } from '../lib/api';
import { formatCountdown, formatDateTime, formatMoney, formatTime } from '../lib/format';
import { TicketList } from './TicketList';

function refFromLocation() {
  return new URLSearchParams(window.location.search).get('ref');
}

function goTo(url) {
  window.location.assign(url);
}

const PROVIDERS = { bkash: 'bKash', paystation: 'PayStation', fake: 'the test gateway' };

const HISTORY = {
  order_created: 'Tickets held',
  payment_started: 'Payment started',
  payment_succeeded: 'Payment confirmed',
  payment_failed: 'Payment not completed',
  payment_abandoned: 'Payment not taken because the hold had ended',
  payment_review: 'Payment under review',
  refund_requested: 'Refund requested',
  refund_completed: 'Refund completed',
  refund_failed: 'Refund passed to our support team',
  order_paid: 'Order paid',
  order_expired: 'Hold expired',
  order_cancelled: 'Tickets released',
};

/**
 * The order page. It reloads the order from the server, so refreshing keeps
 * the order; counts down the hold on the server clock; sends the buyer to the
 * payment provider; and, after they return, polls until the server has
 * confirmed the payment with the provider.
 */
export function OrderView({ getRef = refFromLocation, navigate = goTo, pollMs = 2000, maxPolls = 30 }) {
  const [order, setOrder] = useState(null);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(null);
  const [polls, setPolls] = useState(0);

  useEffect(() => {
    const ref = getRef();
    (ref ? getOrder(ref) : Promise.reject(new ApiError(0, 'missing_ref', 'This link is missing its order reference.')))
      .then(setOrder)
      .catch((err) => setError(err.code === 'order_not_found' ? 'We could not find this order in this browser.' : err.message));
  }, [getRef]);

  const checking = order?.status === 'held' && order.payment_status === 'pending';
  const rendering = order?.status === 'paid' && order.tickets.some((t) => !t.ready);
  const waiting = checking || rendering;
  useEffect(() => {
    if (!waiting || polls >= maxPolls) return undefined;
    const id = setTimeout(() => {
      getOrder(order.ref)
        .then(setOrder)
        .catch(() => {})
        .finally(() => setPolls((n) => n + 1));
    }, pollMs);
    return () => clearTimeout(id);
  }, [waiting, polls, maxPolls, pollMs, order?.ref]);

  async function act(kind, work) {
    setBusy(kind);
    setError(null);
    try {
      await work();
    } catch (err) {
      setError(err.message);
      setBusy(null);
    }
  }

  const pay = () => act('pay', async () => navigate(await startCheckout(order.ref)));
  const [resent, setResent] = useState(false);
  const resend = () =>
    act('resend', async () => {
      await resendTickets(order.ref);
      setResent(true);
      setBusy(null);
    });
  const cancel = () =>
    act('cancel', async () => {
      setOrder(await cancelOrder(order.ref));
      setBusy(null);
    });

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
  const provider = PROVIDERS[order.payment_provider] ?? 'the payment provider';
  return (
    <main className="page order">
      <h1>Your order {order.ref}</h1>
      <Status order={order} onExpire={() => setOrder((o) => ({ ...o, status: 'expired' }))} />
      <PaymentNote order={order} provider={provider} stillChecking={checking && polls < maxPolls} />
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
        <div className="actions">
          <button type="button" className="primary" onClick={pay} disabled={busy !== null}>
            {busy === 'pay'
              ? 'Opening payment…'
              : `${order.payment_status === 'failed' ? 'Try paying again' : 'Pay'} ${money(order.total_minor)}`}
          </button>
          <p className="hint">You finish paying on {provider}&apos;s secure page, then return here.</p>
          <button type="button" className="secondary" onClick={cancel} disabled={busy !== null}>
            {busy === 'cancel' ? 'Releasing…' : 'Release these tickets'}
          </button>
        </div>
      )}
      {order.status === 'paid' && (
        <section aria-labelledby="tickets">
          <h2 id="tickets">Tickets</h2>
          <TicketList tickets={order.tickets} />
          <Deliveries order={order} />
          {resent ? (
            <p role="status">Your tickets are on their way again.</p>
          ) : (
            <button type="button" className="secondary" onClick={resend} disabled={busy !== null}>
              {busy === 'resend' ? 'Sending…' : 'Send my tickets again'}
            </button>
          )}
        </section>
      )}
      <section aria-labelledby="history">
        <h2 id="history">History</h2>
        <ol className="history">
          {order.timeline.map((e) => (
            <li key={`${e.kind}-${e.at}`}>
              <time dateTime={e.at}>{formatTime(e.at, tz)}</time> {HISTORY[e.kind] ?? e.kind}
            </li>
          ))}
        </ol>
      </section>
    </main>
  );
}

function Deliveries({ order }) {
  const line = (channel, sent, failed, pending) => {
    const status = order.deliveries[channel]?.status;
    if (status === 'sent') return sent;
    if (status === 'failed') return failed;
    return pending;
  };
  return (
    <ul className="deliveries">
      <li>
        {line(
          'email',
          `Emailed to ${order.purchaser.email}.`,
          `We could not email ${order.purchaser.email} yet and will keep trying.`,
          `Emailing them to ${order.purchaser.email}…`,
        )}
      </li>
      <li>
        {line(
          'sms',
          `A download link was texted to ${order.purchaser.phone}.`,
          `We could not text ${order.purchaser.phone} yet and will keep trying.`,
          `Texting a download link to ${order.purchaser.phone}…`,
        )}
      </li>
    </ul>
  );
}

function PaymentNote({ order, provider, stillChecking }) {
  const kinds = new Set(order.timeline.map((e) => e.kind));
  let text = null;
  if (order.status === 'held' && order.payment_status === 'pending') {
    text = stillChecking
      ? 'Checking your payment…'
      : `We are still confirming your payment with ${provider}. If it went through, your tickets are issued automatically, so please do not pay twice.`;
  } else if (order.status === 'held' && order.payment_status === 'failed') {
    text = 'Your last payment was not completed and no money was taken. You can try again.';
  } else if (order.payment_status === 'review') {
    text = 'We are checking your payment with the provider. Our support team will contact you; please do not pay again.';
  } else if (kinds.has('refund_completed')) {
    text = 'Your payment arrived after the hold ended and has been refunded in full.';
  } else if (kinds.has('refund_requested')) {
    text = 'Your payment arrived after the hold ended, so it will be refunded in full.';
  }
  return (
    text && (
      <p className="payment-note" role="status">
        {text}
      </p>
    )
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
      <p className="status held" role="timer" aria-live="off">
        Your tickets are held for <strong>{formatCountdown(left)}</strong>, until{' '}
        {formatTime(order.expires_at, order.event.time_zone)}. Complete payment before then.
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

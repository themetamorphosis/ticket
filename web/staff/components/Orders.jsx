'use client';

import { useCallback, useState } from 'react';
import { api } from '../lib/api';
import { money, when } from '../lib/format';
import { ReasonAction } from './ReasonAction';
import { useLoad } from './useLoad';

export function Orders({ me, initialRef = null }) {
  const [query, setQuery] = useState('');
  const [results, setResults] = useState(null);
  const [error, setError] = useState(null);
  const [selected, setSelected] = useState(initialRef);

  async function search(e) {
    e.preventDefault();
    try {
      setResults((await api.searchOrders(query)).orders);
      setError(null);
    } catch (err) {
      setError(err.message);
    }
  }

  return (
    <>
      <h1>Orders</h1>
      <form onSubmit={search} className="row">
        <label>
          Reference, email, phone or name
          <input value={query} onChange={(e) => setQuery(e.target.value)} required />
        </label>
        <button type="submit" className="primary">Search</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {results?.length === 0 && <p>No orders found.</p>}
      {results?.length > 0 && (
        <table>
          <thead><tr><th>Order</th><th>Status</th><th>Buyer</th><th>Tickets</th><th>Total</th></tr></thead>
          <tbody>
            {results.map((o) => (
              <tr key={o.public_ref}>
                <td><button type="button" className="link" onClick={() => setSelected(o.public_ref)}>{o.public_ref}</button></td>
                <td>{o.status}</td>
                <td>{o.purchaser_name} · {o.email} · {o.phone}</td>
                <td>{o.quantity}</td>
                <td>{money(o.total_minor, o.currency)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {selected && <OrderDetail key={selected} orderRef={selected} role={me.staff.role} />}
    </>
  );
}

function OrderDetail({ orderRef, role }) {
  const load = useCallback(() => api.order(orderRef), [orderRef]);
  const { data, error, reload } = useLoad(load);
  if (error) return <p role="alert">{error}</p>;
  if (!data) return <p aria-busy="true">Loading order…</p>;
  const o = data.order;
  const canSupport = role === 'organizer' || role === 'support';
  const canRefund = role === 'organizer' || role === 'finance';
  return (
    <section aria-labelledby="detail">
      <h2 id="detail">Order {o.public_ref}</h2>
      <p>
        {o.status} · {o.purchaser_name} · {o.email} · {o.phone} · {money(o.total_minor, o.currency)} · {when(o.created_at)}
      </p>
      {canSupport && <ReasonAction label="Resend tickets" needsReason={false} action={() => api.resend(o.public_ref)} onDone={reload} />}
      {canRefund && <ReasonAction label="Refund order" action={(r) => api.refund(o.public_ref, r)} onDone={reload} />}
      <h3>Tickets</h3>
      <table>
        <thead><tr><th>#</th><th>Status</th><th>Entry</th><th></th></tr></thead>
        <tbody>
          {data.tickets.map((t) => (
            <tr key={t.id}>
              <td>{t.ordinal}{t.credential_version > 1 && ` (reissued ×${t.credential_version - 1})`}</td>
              <td>{t.status}</td>
              <td>{t.gate ? `${t.gate}, ${when(t.admitted_at)}` : '—'}</td>
              <td>
                {canSupport && t.status === 'valid' && (
                  <>
                    <ReasonAction label="Reissue" action={(r) => api.reissue(t.id, r)} onDone={reload} />
                    <ReasonAction label="Void" action={(r) => api.voidTicket(t.id, r)} onDone={reload} />
                  </>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <h3>Payments</h3>
      <ul>
        {data.attempts.map((a) => (
          <li key={a.created_at}>{a.provider} · {a.status} · {money(a.amount_minor)} · {a.provider_trx_id ?? 'no transaction'} · {when(a.created_at)}</li>
        ))}
        {data.refunds.map((r) => (
          <li key={`${r.reason}-${r.status}`}>Refund ({r.reason}) · {r.status} · {money(r.amount_minor)}{r.last_error && ` · ${r.last_error}`}</li>
        ))}
      </ul>
      <h3>Staff actions</h3>
      {data.history.length === 0 ? (
        <p>None.</p>
      ) : (
        <ul>
          {data.history.map((h) => (
            <li key={`${h.at}-${h.action}-${h.object_id}`}>{when(h.at)} · {h.actor} · {h.action}{h.reason && ` · “${h.reason}”`}</li>
          ))}
        </ul>
      )}
    </section>
  );
}

'use client';

import { useCallback } from 'react';
import { api } from '../lib/api';
import { money, when } from '../lib/format';
import { useLoad } from './useLoad';

export function Finance() {
  const load = useCallback(() => api.finance(), []);
  const { data, error } = useLoad(load);
  if (error) return <p role="alert">{error}</p>;
  if (!data) return <p aria-busy="true">Loading…</p>;
  const l = data.ledger;
  return (
    <>
      <h1>Finance</h1>
      <p role="status" className={data.balanced ? 'ok-text' : 'stop-text'}>
        {data.balanced ? 'The ledger balances.' : 'The ledger does not balance. Investigate before settlement.'}
      </p>
      <dl className="tiles">
        <div><dt>Captured</dt><dd>{money(l.captured_minor)}</dd></div>
        <div><dt>Paid orders</dt><dd>{money(l.paid_orders_minor)}</dd></div>
        <div><dt>Refunded</dt><dd>{money(l.refunded_minor)}</dd></div>
        <div><dt>Refunds open</dt><dd>{money(l.refund_open_minor)}</dd></div>
        <div><dt>Refunds failed</dt><dd>{money(l.refund_failed_minor)}</dd></div>
        <div><dt>Under review</dt><dd>{money(l.review_minor)}</dd></div>
      </dl>
      <h2>Needs attention</h2>
      {data.queue.length === 0 ? (
        <p>Nothing waiting.</p>
      ) : (
        <table>
          <thead><tr><th>Kind</th><th>Order</th><th>Status</th><th>Amount</th><th>Detail</th><th>Updated</th></tr></thead>
          <tbody>
            {data.queue.map((q) => (
              <tr key={`${q.kind}-${q.public_ref}-${q.updated_at}`}>
                <td>{q.kind === 'refund' ? 'Refund' : 'Payment review'}</td>
                <td>{q.public_ref}</td><td>{q.status}</td><td>{money(q.amount_minor)}</td><td>{q.detail}</td><td>{when(q.updated_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

'use client';

import Link from 'next/link';
import { useCallback } from 'react';
import { api } from '../lib/api';
import { money } from '../lib/format';
import { useLoad } from './useLoad';

export function Overview({ me }) {
  const slug = me.events[0]?.slug;
  const role = me.staff.role;
  if (role === 'gate') {
    return (
      <>
        <h1>Welcome, {me.staff.name}</h1>
        <p>
          <Link href="/scan/">Open the scanner</Link>
        </p>
      </>
    );
  }
  if (!slug) return <p>There is no event yet.</p>;
  return <Summary slug={slug} />;
}

function Summary({ slug }) {
  const load = useCallback(() => api.summary(slug), [slug]);
  const { data, error } = useLoad(load);
  if (error) return <p role="alert">{error}</p>;
  if (!data) return <p aria-busy="true">Loading…</p>;
  const s = data.summary;
  return (
    <>
      <h1>{s.title}</h1>
      <p className="hint">
        {s.status === 'published' ? `Published, revision ${s.published_revision}` : 'Draft, not published'}
      </p>
      <dl className="tiles">
        <div><dt>Sold</dt><dd>{s.sold} of {s.capacity}</dd></div>
        <div><dt>Held in checkout</dt><dd>{s.held}</dd></div>
        <div><dt>Remaining</dt><dd>{s.capacity - s.sold - s.held}</dd></div>
        <div><dt>Paid orders</dt><dd>{s.paid_orders}</dd></div>
        <div><dt>Revenue</dt><dd>{money(s.revenue_minor, s.currency)}</dd></div>
        <div><dt>Admitted</dt><dd>{s.admitted}</dd></div>
        <div><dt>Delivery problems</dt><dd>{s.failed_delivery}</dd></div>
      </dl>
      <h2>Gates</h2>
      {data.gates.length === 0 ? (
        <p>No gates yet.</p>
      ) : (
        <table>
          <thead><tr><th>Gate</th><th>Admitted</th><th>Refused</th></tr></thead>
          <tbody>
            {data.gates.map((g) => (
              <tr key={g.gate}><td>{g.gate}</td><td>{g.admitted}</td><td>{g.refused}</td></tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

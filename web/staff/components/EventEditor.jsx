'use client';

import { useCallback, useState } from 'react';
import { api } from '../lib/api';
import { when } from '../lib/format';
import { useLoad } from './useLoad';

const FIELDS = [
  ['title', 'Title', 'text'],
  ['organizer', 'Organizer', 'text'],
  ['venue_name', 'Venue', 'text'],
  ['venue_address', 'Venue address', 'text'],
  ['description', 'Description', 'text'],
  ['starts_at', 'Starts (UTC, ISO 8601)', 'text'],
  ['time_zone', 'Time zone', 'text'],
  ['sale_opens_at', 'Sale opens (UTC)', 'text'],
  ['sale_closes_at', 'Sale closes (UTC)', 'text'],
  ['max_per_order', 'Tickets per order', 'number'],
  ['min_age', 'Minimum age', 'number'],
  ['price_minor', 'Price (paisa)', 'number'],
  ['fee_minor', 'Fee (paisa)', 'number'],
];

function toForm(data) {
  const e = data.event;
  const tier = data.tiers[0];
  return {
    title: e.title, organizer: e.organizer, venue_name: e.venue_name, venue_address: e.venue_address,
    description: e.description, lineup: e.lineup.join(', '), starts_at: e.starts_at, doors_at: e.doors_at,
    time_zone: e.time_zone, sale_opens_at: e.sale_opens_at, sale_closes_at: e.sale_closes_at,
    max_per_order: e.max_per_order, min_age: e.min_age, price_minor: tier.price_minor, fee_minor: tier.fee_minor,
  };
}

export function EventEditor({ me }) {
  const slug = me.events[0]?.slug;
  const load = useCallback(() => (slug ? api.event(slug) : Promise.resolve(null)), [slug]);
  const { data, error, reload } = useLoad(load);
  if (!slug) return <p>There is no event yet.</p>;
  if (error) return <p role="alert">{error}</p>;
  if (!data) return <p aria-busy="true">Loading…</p>;
  return <Editor key={data.event.published_revision} slug={slug} data={data} reload={reload} />;
}

function Editor({ slug, data, reload }) {
  const [form, setForm] = useState(() => toForm(data));
  const [capacity, setCapacity] = useState(String(data.event.capacity));
  const [message, setMessage] = useState(null);
  const run = (work, done) => async (e) => {
    e.preventDefault();
    try {
      await work();
      setMessage(done);
      reload();
    } catch (err) {
      setMessage(err.message);
    }
  };
  const save = run(async () => {
    const lineup = form.lineup.split(',').map((s) => s.trim()).filter(Boolean);
    const numbers = Object.fromEntries(['max_per_order', 'min_age', 'price_minor', 'fee_minor'].map((k) => [k, Number(form[k])]));
    await api.updateEvent(slug, { ...form, ...numbers, lineup });
  }, 'Saved. Publish to show the changes to buyers.');
  const resize = run(() => api.setCapacity(slug, Number(capacity)), 'Capacity updated.');
  const publish = run(() => api.publish(slug), 'Published.');

  return (
    <>
      <h1>Event</h1>
      <p className="hint">
        {data.event.status === 'published' ? `Published revision ${data.event.published_revision}, ${when(data.event.published_at)}` : 'Not published'}
      </p>
      {message && <p role="status">{message}</p>}
      <form onSubmit={save} className="stack">
        {FIELDS.map(([key, label, type]) => (
          <label key={key}>
            {label}
            <input type={type} value={form[key]} onChange={(e) => setForm({ ...form, [key]: e.target.value })} />
          </label>
        ))}
        <label>
          Lineup (comma separated)
          <input value={form.lineup} onChange={(e) => setForm({ ...form, lineup: e.target.value })} />
        </label>
        <button type="submit" className="primary">Save details</button>
      </form>
      <form onSubmit={resize} className="row">
        <label>
          Capacity
          <input type="number" min="0" value={capacity} onChange={(e) => setCapacity(e.target.value)} />
        </label>
        <button type="submit" className="secondary">Change capacity</button>
      </form>
      <form onSubmit={publish}>
        <button type="submit" className="primary">Publish to the buyer site</button>
      </form>
    </>
  );
}

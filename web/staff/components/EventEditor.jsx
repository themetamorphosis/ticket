'use client';

import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import { useCallback, useState } from 'react';
import { api } from '../lib/api';
import { when } from '../lib/format';
import { PageContainer } from './PageContainer';
import { PageHeader } from './PageHeader';
import { EmptyState, LoadingState } from './States';
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
  if (data) return <Editor key={data.event.published_revision} slug={slug} data={data} reload={reload} />;
  let body = <LoadingState />;
  if (!slug) body = <EmptyState title="There is no event yet." />;
  else if (error) body = <Alert severity="error">{error}</Alert>;
  return (
    <PageContainer>
      <PageHeader title="Event" />
      {body}
    </PageContainer>
  );
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
  const e = data.event;

  return (
    <PageContainer>
      <PageHeader
        title="Event"
        subtitle={e.status === 'published' ? `Published revision ${e.published_revision}, ${when(e.published_at)}` : 'Not published'}
        actions={<Button variant="contained" onClick={publish}>Publish to the buyer site</Button>}
      />
      {message && <Alert severity="info" role="status" sx={{ mb: 3 }}>{message}</Alert>}
      <Stack direction={{ xs: 'column', lg: 'row' }} sx={{ gap: 3, alignItems: 'flex-start' }}>
        <Card variant="outlined" sx={{ flex: 1, width: '100%' }}>
          <CardContent>
            <Typography variant="h6" component="h2" sx={{ mb: 2 }}>Details</Typography>
            <Box
              component="form"
              onSubmit={save}
              sx={{ display: 'grid', gap: 2, gridTemplateColumns: { xs: '1fr', md: 'repeat(2, minmax(0, 1fr))' } }}
            >
              {FIELDS.map(([key, label, type]) => (
                <TextField key={key} label={label} type={type} value={form[key]} onChange={(ev) => setForm({ ...form, [key]: ev.target.value })} />
              ))}
              <TextField
                label="Lineup (comma separated)"
                value={form.lineup}
                onChange={(ev) => setForm({ ...form, lineup: ev.target.value })}
                sx={{ gridColumn: '1 / -1' }}
              />
              <Box sx={{ gridColumn: '1 / -1' }}>
                <Button type="submit" variant="contained">Save details</Button>
              </Box>
            </Box>
          </CardContent>
        </Card>
        <Card variant="outlined" sx={{ width: { xs: '100%', lg: 320 } }}>
          <CardContent>
            <Typography variant="h6" component="h2" sx={{ mb: 2 }}>Capacity</Typography>
            <Stack component="form" onSubmit={resize} spacing={2}>
              <TextField
                label="Capacity"
                type="number"
                value={capacity}
                onChange={(ev) => setCapacity(ev.target.value)}
                slotProps={{ htmlInput: { min: 0 } }}
              />
              <Button type="submit" variant="outlined">Change capacity</Button>
            </Stack>
          </CardContent>
        </Card>
      </Stack>
    </PageContainer>
  );
}

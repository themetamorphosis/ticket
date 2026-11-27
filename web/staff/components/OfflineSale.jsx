'use client';

import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import FormControl from '@mui/material/FormControl';
import FormControlLabel from '@mui/material/FormControlLabel';
import FormHelperText from '@mui/material/FormHelperText';
import FormLabel from '@mui/material/FormLabel';
import Radio from '@mui/material/Radio';
import RadioGroup from '@mui/material/RadioGroup';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import { useState } from 'react';
import { api } from '../lib/api';
import { money } from '../lib/format';
import { PageContainer } from './PageContainer';
import { PageHeader } from './PageHeader';
import { EmptyState } from './States';

export const METHODS = [
  ['cash', 'Cash'],
  ['bank', 'Bank transfer'],
  ['bkash', 'bKash'],
];
const LABELS = Object.fromEntries(METHODS);

const EMPTY = { quantity: '1', name: '', email: '', phone: '', age: '', method: 'cash', reference: '' };

const FIELDS = [
  ['name', 'Buyer name', 'text', 'name'],
  ['phone', 'Mobile number', 'tel', 'tel'],
  ['email', 'Email', 'email', 'email'],
  ['age', 'Age', 'number', 'off'],
];

// One key per sale: a retry after a lost response returns the first sale.
const newKey = () => `offline-${crypto.randomUUID()}`;

const SUBTITLE = 'Record tickets paid in cash, by bank transfer or to the bKash number. They are emailed and texted like online tickets.';

export function OfflineSale({ me }) {
  const event = me.events[0];
  if (!event) {
    return (
      <PageContainer>
        <PageHeader title="Sell offline" subtitle={SUBTITLE} />
        <EmptyState title="There is no event yet." />
      </PageContainer>
    );
  }
  return <SaleForm event={event} />;
}

function SaleForm({ event }) {
  const [form, setForm] = useState(EMPTY);
  const [key, setKey] = useState(newKey);
  const [fields, setFields] = useState({});
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);
  const [sold, setSold] = useState(null);
  const set = (name) => (e) => setForm({ ...form, [name]: e.target.value });
  const total = (Number(form.quantity) || 0) * event.price_minor;

  async function submit(e) {
    e.preventDefault();
    setBusy(true);
    try {
      const order = await api.sellOffline(
        event.slug,
        { ...form, quantity: Number(form.quantity), age: Number(form.age) },
        key,
      );
      setSold({ order, method: form.method });
      setFields({});
      setError(null);
    } catch (err) {
      setFields(err.details?.fields ?? {});
      if (err.code === 'idempotency_key_reused') {
        // The details changed after an attempt whose outcome we never saw.
        setKey(newKey());
        setError('An earlier attempt with different details may already be recorded. Search Orders for this buyer before selling again.');
      } else {
        setError(err.message);
      }
    } finally {
      setBusy(false);
    }
  }

  function another() {
    setForm(EMPTY);
    setKey(newKey());
    setSold(null);
  }

  if (sold) {
    const o = sold.order;
    return (
      <PageContainer>
        <PageHeader title="Sell offline" subtitle={SUBTITLE} />
        <Card variant="outlined" component="section" aria-labelledby="sold" sx={{ maxWidth: 640 }}>
          <CardContent>
            <Typography id="sold" variant="h6" component="h2">
              Sale recorded: order {o.ref}
            </Typography>
            <Typography sx={{ mt: 1 }}>
              {o.quantity} {o.quantity === 1 ? 'ticket' : 'tickets'} · {money(o.total_minor, o.currency)} by {LABELS[sold.method]}
            </Typography>
            <Typography color="text.secondary" sx={{ mt: 1, overflowWrap: 'anywhere' }}>
              The tickets are being emailed to {o.purchaser.email}, and a download link is texted to {o.purchaser.phone}.
            </Typography>
            <Button variant="contained" onClick={another} sx={{ mt: 2 }}>
              Record another sale
            </Button>
          </CardContent>
        </Card>
      </PageContainer>
    );
  }

  return (
    <PageContainer>
      <PageHeader title="Sell offline" subtitle={SUBTITLE} />
      <Card variant="outlined" sx={{ maxWidth: 720 }}>
        <CardContent>
          <Typography variant="h6" component="h2">{event.title}</Typography>
          <Typography color="text.secondary" sx={{ mb: 2 }}>
            {money(event.price_minor, event.currency)} per ticket, no online fee.
          </Typography>
          {error && <Alert severity="error" sx={{ mb: 2 }}>{error}</Alert>}
          <Box
            component="form"
            onSubmit={submit}
            noValidate
            sx={{ display: 'grid', gap: 2, gridTemplateColumns: { xs: '1fr', md: 'repeat(2, minmax(0, 1fr))' } }}
          >
            {FIELDS.map(([name, label, type, autoComplete]) => (
              <TextField
                key={name}
                label={label}
                type={type}
                value={form[name]}
                onChange={set(name)}
                autoComplete={autoComplete}
                error={Boolean(fields[name])}
                helperText={fields[name] ?? (name === 'phone' ? 'For example +8801XXXXXXXXX' : ' ')}
                required
              />
            ))}
            <TextField
              label="Tickets"
              type="number"
              value={form.quantity}
              onChange={set('quantity')}
              error={Boolean(fields.quantity)}
              helperText={fields.quantity ?? ' '}
              slotProps={{ htmlInput: { min: 1 } }}
              required
            />
            <FormControl error={Boolean(fields.method)} sx={{ gridColumn: '1 / -1' }}>
              <FormLabel id="method">Paid by</FormLabel>
              <RadioGroup row aria-labelledby="method" value={form.method} onChange={set('method')}>
                {METHODS.map(([value, label]) => (
                  <FormControlLabel key={value} value={value} control={<Radio />} label={label} />
                ))}
              </RadioGroup>
              {fields.method && <FormHelperText>{fields.method}</FormHelperText>}
            </FormControl>
            <TextField
              label="Payment reference (optional)"
              value={form.reference}
              onChange={set('reference')}
              error={Boolean(fields.reference)}
              helperText={fields.reference ?? 'bKash TrxID, bank reference or receipt number'}
              sx={{ gridColumn: '1 / -1' }}
            />
            <Stack direction="row" sx={{ gridColumn: '1 / -1', alignItems: 'center', gap: 2, flexWrap: 'wrap' }}>
              <Button type="submit" variant="contained" disabled={busy}>
                Record sale and send tickets
              </Button>
              <Typography aria-live="polite" sx={{ fontWeight: 600 }}>
                Collect {money(total, event.currency)}
              </Typography>
            </Stack>
          </Box>
        </CardContent>
      </Card>
    </PageContainer>
  );
}

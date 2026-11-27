'use client';

import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import Link from '@mui/material/Link';
import List from '@mui/material/List';
import ListItem from '@mui/material/ListItem';
import ListItemText from '@mui/material/ListItemText';
import Paper from '@mui/material/Paper';
import Stack from '@mui/material/Stack';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableContainer from '@mui/material/TableContainer';
import TableHead from '@mui/material/TableHead';
import TableRow from '@mui/material/TableRow';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import { useCallback, useState } from 'react';
import { api } from '../lib/api';
import { money, when } from '../lib/format';
import { Search as SearchIcon } from './icons';
import { PageContainer } from './PageContainer';
import { PageHeader } from './PageHeader';
import { ReasonAction } from './ReasonAction';
import { DesktopOnly, MobileOnly, RecordCard } from './RecordList';
import { EmptyState, LoadingState } from './States';
import { StatusChip } from './StatusChip';
import { useLoad } from './useLoad';

const PAID_BY = { cash: 'cash', bank: 'bank transfer', bkash: 'bKash' };

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
    <PageContainer>
      <PageHeader title="Orders" subtitle="Find an order, resend tickets, refund or reissue." />
      <Stack component="form" onSubmit={search} direction={{ xs: 'column', sm: 'row' }} sx={{ gap: 1.5, mb: 3, maxWidth: 640 }}>
        <TextField
          label="Reference, email, phone or name"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          required
          fullWidth
          size="small"
        />
        <Button type="submit" variant="contained" startIcon={<SearchIcon />}>
          Search
        </Button>
      </Stack>
      {error && <Alert severity="error" sx={{ mb: 3 }}>{error}</Alert>}
      {results?.length === 0 && <EmptyState title="No orders found." />}
      {results?.length > 0 && <Results results={results} onOpen={setSelected} />}
      {selected && <OrderDetail key={selected} orderRef={selected} role={me.staff.role} />}
    </PageContainer>
  );
}

function Results({ results, onOpen }) {
  const open = (o) => (
    <Link component="button" type="button" onClick={() => onOpen(o.public_ref)} sx={{ fontWeight: 600 }}>
      {o.public_ref}
    </Link>
  );
  return (
    <>
      <DesktopOnly>
        <TableContainer component={Paper} variant="outlined" sx={{ mb: 3 }}>
          <Table size="small" sx={{ minWidth: 760 }}>
            <TableHead>
              <TableRow>
                <TableCell>Order</TableCell>
                <TableCell>Status</TableCell>
                <TableCell>Buyer</TableCell>
                <TableCell align="right">Tickets</TableCell>
                <TableCell align="right">Total</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {results.map((o) => (
                <TableRow key={o.public_ref}>
                  <TableCell>{open(o)}</TableCell>
                  <TableCell><StatusChip status={o.status} /></TableCell>
                  <TableCell>{o.purchaser_name} · {o.email} · {o.phone}</TableCell>
                  <TableCell align="right">{o.quantity}</TableCell>
                  <TableCell align="right">{money(o.total_minor, o.currency)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>
      </DesktopOnly>
      <MobileOnly>
        {results.map((o) => (
          <RecordCard
            key={o.public_ref}
            title={open(o)}
            badge={<StatusChip status={o.status} />}
            fields={[['Buyer', o.purchaser_name], ['Contact', `${o.email} · ${o.phone}`], ['Total', `${money(o.total_minor, o.currency)} · ${o.quantity} tickets`]]}
          />
        ))}
      </MobileOnly>
    </>
  );
}

function Section({ title, children }) {
  return (
    <>
      <Typography variant="subtitle1" component="h3" sx={{ fontWeight: 600, mt: 3, mb: 1 }}>
        {title}
      </Typography>
      {children}
    </>
  );
}

function OrderDetail({ orderRef, role }) {
  const load = useCallback(() => api.order(orderRef), [orderRef]);
  const { data, error, reload } = useLoad(load);
  if (error) return <Alert severity="error" sx={{ mt: 3 }}>{error}</Alert>;
  if (!data) return <LoadingState label="Loading order…" />;
  const o = data.order;
  const canSupport = role === 'organizer' || role === 'support';
  const canRefund = role === 'organizer' || role === 'finance';
  return (
    <Card variant="outlined" component="section" aria-labelledby="detail" sx={{ mt: 3 }}>
      <CardContent>
        <Stack direction="row" sx={{ alignItems: 'center', gap: 1.5, flexWrap: 'wrap' }}>
          <Typography id="detail" variant="h6" component="h2">
            Order {o.public_ref}
          </Typography>
          <StatusChip status={o.status} />
        </Stack>
        <Typography color="text.secondary" sx={{ mt: 0.5, overflowWrap: 'anywhere' }}>
          {o.purchaser_name} · {o.email} · {o.phone} · {money(o.total_minor, o.currency)} · {when(o.created_at)}
        </Typography>
        <Stack direction="row" sx={{ gap: 1.5, flexWrap: 'wrap', mt: 2 }}>
          {canSupport && <ReasonAction label="Resend tickets" needsReason={false} action={() => api.resend(o.public_ref)} onDone={reload} />}
          {canRefund && <ReasonAction label="Refund order" destructive action={(r) => api.refund(o.public_ref, r)} onDone={reload} />}
        </Stack>

        <Section title="Tickets">
          <TableContainer component={Paper} variant="outlined">
            <Table size="small" sx={{ minWidth: 520 }}>
              <TableHead>
                <TableRow>
                  <TableCell>#</TableCell>
                  <TableCell>Status</TableCell>
                  <TableCell>Entry</TableCell>
                  <TableCell aria-label="Actions" />
                </TableRow>
              </TableHead>
              <TableBody>
                {data.tickets.map((t) => (
                  <TableRow key={t.id}>
                    <TableCell>{t.ordinal}{t.credential_version > 1 && ` (reissued ×${t.credential_version - 1})`}</TableCell>
                    <TableCell><StatusChip status={t.status} /></TableCell>
                    <TableCell>{t.gate ? `${t.gate}, ${when(t.admitted_at)}` : '—'}</TableCell>
                    <TableCell>
                      {canSupport && t.status === 'valid' && (
                        <Stack direction="row" sx={{ gap: 1, flexWrap: 'wrap' }}>
                          <ReasonAction label="Reissue" action={(r) => api.reissue(t.id, r)} onDone={reload} />
                          <ReasonAction label="Void" destructive action={(r) => api.voidTicket(t.id, r)} onDone={reload} />
                        </Stack>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableContainer>
        </Section>

        <Section title="Payments">
          <List dense disablePadding>
            {data.attempts.map((a) => (
              <ListItem key={a.created_at} disableGutters>
                {a.offline_method ? (
                  <ListItemText
                    primary={`Offline, ${PAID_BY[a.offline_method]} · ${a.status} · ${money(a.amount_minor)}`}
                    secondary={`Received by ${a.received_by} · ${a.offline_reference ?? 'no reference'} · receipt ${a.provider_trx_id} · ${when(a.created_at)}`}
                  />
                ) : (
                  <ListItemText
                    primary={`${a.provider} · ${a.status} · ${money(a.amount_minor)}`}
                    secondary={`${a.provider_trx_id ?? 'no transaction'} · ${when(a.created_at)}`}
                  />
                )}
              </ListItem>
            ))}
            {data.refunds.map((r) => (
              <ListItem key={`${r.reason}-${r.status}`} disableGutters>
                <ListItemText
                  primary={`Refund (${r.reason}) · ${r.status} · ${money(r.amount_minor)}`}
                  secondary={r.last_error}
                />
              </ListItem>
            ))}
          </List>
        </Section>

        <Section title="Staff actions">
          {data.history.length === 0 ? (
            <Typography color="text.secondary">None.</Typography>
          ) : (
            <List dense disablePadding>
              {data.history.map((h) => (
                <ListItem key={`${h.at}-${h.action}-${h.object_id}`} disableGutters>
                  <ListItemText
                    primary={`${h.actor} · ${h.action}${h.reason && ` · “${h.reason}”`}`}
                    secondary={when(h.at)}
                  />
                </ListItem>
              ))}
            </List>
          )}
        </Section>
      </CardContent>
    </Card>
  );
}

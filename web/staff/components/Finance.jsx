'use client';

import Alert from '@mui/material/Alert';
import Paper from '@mui/material/Paper';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableContainer from '@mui/material/TableContainer';
import TableHead from '@mui/material/TableHead';
import TableRow from '@mui/material/TableRow';
import Typography from '@mui/material/Typography';
import { useCallback } from 'react';
import { api } from '../lib/api';
import { money, when } from '../lib/format';
import { PageContainer } from './PageContainer';
import { PageHeader } from './PageHeader';
import { DesktopOnly, MobileOnly, RecordCard } from './RecordList';
import { StatCard, StatGrid } from './StatCard';
import { EmptyState, LoadingState } from './States';
import { StatusChip } from './StatusChip';
import { useLoad } from './useLoad';

const kind = (q) => (q.kind === 'refund' ? 'Refund' : 'Payment review');
const key = (q) => `${q.kind}-${q.public_ref}-${q.updated_at}`;

export function Finance() {
  const load = useCallback(() => api.finance(), []);
  const { data, error } = useLoad(load);
  return (
    <PageContainer>
      <PageHeader title="Finance" subtitle="Ledger totals and the work waiting on finance." />
      {error && <Alert severity="error">{error}</Alert>}
      {!error && !data && <LoadingState />}
      {data && <Ledger data={data} />}
    </PageContainer>
  );
}

function Ledger({ data }) {
  const l = data.ledger;
  return (
    <>
      <Alert severity={data.balanced ? 'success' : 'error'} role="status" sx={{ mb: 3 }}>
        {data.balanced ? 'The ledger balances.' : 'The ledger does not balance. Investigate before settlement.'}
      </Alert>
      <StatGrid>
        <StatCard label="Captured" value={money(l.captured_minor)} />
        <StatCard label="Paid orders" value={money(l.paid_orders_minor)} />
        <StatCard label="Refunded" value={money(l.refunded_minor)} />
        <StatCard label="Refunds open" value={money(l.refund_open_minor)} />
        <StatCard label="Refunds failed" value={money(l.refund_failed_minor)} />
        <StatCard label="Under review" value={money(l.review_minor)} />
      </StatGrid>
      <Typography variant="h6" component="h2" sx={{ mb: 1.5 }}>
        Needs attention
      </Typography>
      {data.queue.length === 0 ? (
        <EmptyState title="Nothing waiting." />
      ) : (
        <>
          <DesktopOnly>
            <TableContainer component={Paper} variant="outlined">
              <Table size="small" sx={{ minWidth: 840 }}>
                <TableHead>
                  <TableRow>
                    <TableCell>Kind</TableCell>
                    <TableCell>Order</TableCell>
                    <TableCell>Status</TableCell>
                    <TableCell align="right">Amount</TableCell>
                    <TableCell>Detail</TableCell>
                    <TableCell>Updated</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {data.queue.map((q) => (
                    <TableRow key={key(q)}>
                      <TableCell>{kind(q)}</TableCell>
                      <TableCell>{q.public_ref}</TableCell>
                      <TableCell><StatusChip status={q.status} /></TableCell>
                      <TableCell align="right">{money(q.amount_minor)}</TableCell>
                      <TableCell>{q.detail}</TableCell>
                      <TableCell sx={{ whiteSpace: 'nowrap' }}>{when(q.updated_at)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableContainer>
          </DesktopOnly>
          <MobileOnly>
            {data.queue.map((q) => (
              <RecordCard
                key={key(q)}
                title={q.public_ref}
                badge={<StatusChip status={q.status} />}
                fields={[['Kind', kind(q)], ['Amount', money(q.amount_minor)], ['Detail', q.detail], ['Updated', when(q.updated_at)]]}
              />
            ))}
          </MobileOnly>
        </>
      )}
    </>
  );
}

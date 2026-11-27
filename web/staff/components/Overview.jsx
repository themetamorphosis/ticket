'use client';

import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Paper from '@mui/material/Paper';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableContainer from '@mui/material/TableContainer';
import TableHead from '@mui/material/TableHead';
import TableRow from '@mui/material/TableRow';
import Typography from '@mui/material/Typography';
import Link from 'next/link';
import { useCallback } from 'react';
import { api } from '../lib/api';
import { money } from '../lib/format';
import { QrCodeScanner as QrCodeScannerIcon } from './icons';
import { PageContainer } from './PageContainer';
import { PageHeader } from './PageHeader';
import { StatCard, StatGrid } from './StatCard';
import { EmptyState, LoadingState } from './States';
import { useLoad } from './useLoad';

export function Overview({ me }) {
  const slug = me.events[0]?.slug;
  if (me.staff.role === 'gate') {
    return (
      <PageContainer>
        <PageHeader title={`Welcome, ${me.staff.name}`} subtitle="Admit ticket holders at your gate." />
        <Button component={Link} href="/scan/" variant="contained" size="large" startIcon={<QrCodeScannerIcon />}>
          Open the scanner
        </Button>
      </PageContainer>
    );
  }
  if (!slug) {
    return (
      <PageContainer>
        <PageHeader title="Overview" />
        <EmptyState title="There is no event yet." />
      </PageContainer>
    );
  }
  return <Summary slug={slug} />;
}

function Summary({ slug }) {
  const load = useCallback(() => api.summary(slug), [slug]);
  const { data, error } = useLoad(load);
  if (!data) {
    return (
      <PageContainer>
        <PageHeader title="Overview" />
        {error ? <Alert severity="error">{error}</Alert> : <LoadingState />}
      </PageContainer>
    );
  }
  const s = data.summary;
  return (
    <PageContainer>
      <PageHeader
        title={s.title}
        subtitle={s.status === 'published' ? `Published, revision ${s.published_revision}` : 'Draft, not published'}
      />
      <StatGrid>
        <StatCard label="Sold" value={`${s.sold} of ${s.capacity}`} />
        <StatCard label="Held in checkout" value={s.held} />
        <StatCard label="Remaining" value={s.capacity - s.sold - s.held} />
        <StatCard label="Paid orders" value={s.paid_orders} />
        <StatCard label="Revenue" value={money(s.revenue_minor, s.currency)} />
        <StatCard label="Admitted" value={s.admitted} />
        <StatCard label="Delivery problems" value={s.failed_delivery} />
      </StatGrid>
      <Typography variant="h6" component="h2" sx={{ mb: 1.5 }}>
        Gates
      </Typography>
      {data.gates.length === 0 ? (
        <EmptyState title="No gates yet." />
      ) : (
        <TableContainer component={Paper} variant="outlined">
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell>Gate</TableCell>
                <TableCell align="right">Admitted</TableCell>
                <TableCell align="right">Refused</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {data.gates.map((g) => (
                <TableRow key={g.gate}>
                  <TableCell>{g.gate}</TableCell>
                  <TableCell align="right">{g.admitted}</TableCell>
                  <TableCell align="right">{g.refused}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>
      )}
    </PageContainer>
  );
}

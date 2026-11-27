'use client';

import Alert from '@mui/material/Alert';
import Paper from '@mui/material/Paper';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableContainer from '@mui/material/TableContainer';
import TableHead from '@mui/material/TableHead';
import TableRow from '@mui/material/TableRow';
import { useCallback } from 'react';
import { api } from '../lib/api';
import { when } from '../lib/format';
import { PageContainer } from './PageContainer';
import { PageHeader } from './PageHeader';
import { DesktopOnly, MobileOnly, RecordCard } from './RecordList';
import { LoadingState } from './States';
import { useLoad } from './useLoad';

const object = (e) => `${e.object_kind} ${e.object_id.slice(0, 8)}`;
const key = (e) => `${e.at}-${e.action}-${e.object_id}`;

export function AuditLog() {
  const load = useCallback(() => api.audit(), []);
  const { data, error } = useLoad(load);
  return (
    <PageContainer>
      <PageHeader title="Audit log" subtitle="Recent staff actions, newest first." />
      {error && <Alert severity="error">{error}</Alert>}
      {!error && !data && <LoadingState />}
      {data && (
        <>
          <DesktopOnly>
            <TableContainer component={Paper} variant="outlined">
              <Table size="small" sx={{ minWidth: 760 }}>
                <TableHead>
                  <TableRow>
                    <TableCell>When</TableCell>
                    <TableCell>Who</TableCell>
                    <TableCell>Action</TableCell>
                    <TableCell>Object</TableCell>
                    <TableCell>Reason</TableCell>
                  </TableRow>
                </TableHead>
                <TableBody>
                  {data.entries.map((e) => (
                    <TableRow key={key(e)}>
                      <TableCell sx={{ whiteSpace: 'nowrap' }}>{when(e.at)}</TableCell>
                      <TableCell>{e.actor}</TableCell>
                      <TableCell>{e.action}</TableCell>
                      <TableCell>{object(e)}</TableCell>
                      <TableCell>{e.reason}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableContainer>
          </DesktopOnly>
          <MobileOnly>
            {data.entries.map((e) => (
              <RecordCard
                key={key(e)}
                title={e.action}
                fields={[['When', when(e.at)], ['Who', e.actor], ['Object', object(e)], ['Reason', e.reason || '—']]}
              />
            ))}
          </MobileOnly>
        </>
      )}
    </PageContainer>
  );
}

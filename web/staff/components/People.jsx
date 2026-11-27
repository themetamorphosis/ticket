'use client';

import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import List from '@mui/material/List';
import ListItem from '@mui/material/ListItem';
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
import { PageContainer } from './PageContainer';
import { PageHeader } from './PageHeader';
import { ReasonAction } from './ReasonAction';
import { DesktopOnly, MobileOnly, RecordCard } from './RecordList';
import { EmptyState, LoadingState } from './States';
import { StatusChip } from './StatusChip';
import { useLoad } from './useLoad';

const ROLES = [
  ['gate', 'Gate'],
  ['support', 'Support'],
  ['finance', 'Finance'],
  ['organizer', 'Organizer'],
];

function Heading({ children }) {
  return (
    <Typography variant="h6" component="h2" sx={{ mt: 4, mb: 1.5 }}>
      {children}
    </Typography>
  );
}

export function People({ me }) {
  const slug = me.events[0]?.slug;
  const loadGates = useCallback(() => api.gates(slug), [slug]);
  const loadStaff = useCallback(() => api.staff(), []);
  const gates = useLoad(loadGates);
  const staff = useLoad(loadStaff);
  const [gateName, setGateName] = useState('');
  const [invite, setInvite] = useState({ email: '', name: '', role: 'gate' });
  const [inviteURL, setInviteURL] = useState(null);
  const [message, setMessage] = useState(null);

  const run = (work, after) => async (e) => {
    e.preventDefault();
    try {
      const res = await work();
      setMessage(null);
      after(res);
    } catch (err) {
      setMessage(err.message);
    }
  };

  const failed = gates.error ?? staff.error;
  const ready = gates.data && staff.data;
  return (
    <PageContainer>
      <PageHeader title="Gates and staff" subtitle="Who works which gate, and who can sign in." />
      {failed && <Alert severity="error">{failed}</Alert>}
      {!failed && !ready && <LoadingState />}
      {!failed && ready && (
        <>
          {message && <Alert severity="error" sx={{ mb: 2 }}>{message}</Alert>}
          <Heading>Gates</Heading>
          {gates.data.gates.length === 0 && <EmptyState title="No gates yet." />}
          <Paper variant="outlined" sx={{ display: gates.data.gates.length === 0 ? 'none' : 'block' }}>
            <List disablePadding>
              {gates.data.gates.map((g, i) => (
                <ListItem key={g.id} divider={i < gates.data.gates.length - 1} sx={{ flexWrap: 'wrap', gap: 1.5 }}>
                  <Box sx={{ flex: '1 1 12rem', minWidth: 0 }}>
                    <Typography sx={{ fontWeight: 600 }}>{g.name}</Typography>
                    <Typography variant="body2" color="text.secondary">
                      {g.staff.length ? g.staff.join(', ') : 'nobody assigned'}
                    </Typography>
                  </Box>
                  <AssignForm
                    people={staff.data.staff.filter((s) => s.role === 'gate' && s.status === 'active')}
                    onAssign={(id) => api.assign(g.id, id).then(gates.reload, (err) => setMessage(err.message))}
                  />
                </ListItem>
              ))}
            </List>
          </Paper>
          <Stack
            component="form"
            direction="row"
            onSubmit={run(() => api.addGate(slug, gateName), () => { setGateName(''); gates.reload(); })}
            sx={{ gap: 1.5, mt: 2, maxWidth: 480, alignItems: 'center' }}
          >
            <TextField label="New gate" value={gateName} onChange={(e) => setGateName(e.target.value)} required size="small" fullWidth />
            <Button type="submit" variant="outlined" sx={{ flexShrink: 0 }}>Add gate</Button>
          </Stack>

          <Heading>Staff</Heading>
          <StaffList people={staff.data.staff} me={me} onChanged={staff.reload} />

          <Card variant="outlined" sx={{ mt: 4, maxWidth: 560 }}>
            <CardContent>
              <Stack
                component="form"
                spacing={2}
                onSubmit={run(() => api.invite(invite.email, invite.name, invite.role), (res) => { setInviteURL(res.invite_url); staff.reload(); })}
              >
                <Typography variant="subtitle1" component="h3" sx={{ fontWeight: 600 }}>Invite someone</Typography>
                <TextField label="Name" value={invite.name} onChange={(e) => setInvite({ ...invite, name: e.target.value })} required />
                <TextField label="Email" type="email" value={invite.email} onChange={(e) => setInvite({ ...invite, email: e.target.value })} required />
                <TextField
                  select
                  label="Role"
                  value={invite.role}
                  onChange={(e) => setInvite({ ...invite, role: e.target.value })}
                  slotProps={{ select: { native: true } }}
                >
                  {ROLES.map(([value, label]) => (
                    <option key={value} value={value}>{label}</option>
                  ))}
                </TextField>
                <Button type="submit" variant="contained" sx={{ alignSelf: 'flex-start' }}>Create invitation</Button>
              </Stack>
              {inviteURL && (
                <Alert severity="success" role="status" sx={{ mt: 2, '& code': { overflowWrap: 'anywhere' } }}>
                  Send this one-time link to them privately (valid 72 hours): <code>{inviteURL}</code>
                </Alert>
              )}
            </CardContent>
          </Card>
        </>
      )}
    </PageContainer>
  );
}

function StaffList({ people, me, onChanged }) {
  const revoke = (s) =>
    s.status !== 'disabled' && s.id !== me.staff.id && (
      <ReasonAction label="Revoke access" destructive action={(r) => api.disable(s.id, r)} onDone={onChanged} />
    );
  return (
    <>
      <DesktopOnly>
        <TableContainer component={Paper} variant="outlined">
          <Table size="small" sx={{ minWidth: 720 }}>
            <TableHead>
              <TableRow>
                <TableCell>Name</TableCell>
                <TableCell>Email</TableCell>
                <TableCell>Role</TableCell>
                <TableCell>Status</TableCell>
                <TableCell aria-label="Actions" />
              </TableRow>
            </TableHead>
            <TableBody>
              {people.map((s) => (
                <TableRow key={s.id}>
                  <TableCell>{s.name}</TableCell>
                  <TableCell>{s.email}</TableCell>
                  <TableCell sx={{ textTransform: 'capitalize' }}>{s.role}</TableCell>
                  <TableCell><StatusChip status={s.status} /></TableCell>
                  <TableCell>{revoke(s)}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableContainer>
      </DesktopOnly>
      <MobileOnly>
        {people.map((s) => (
          <RecordCard
            key={s.id}
            title={s.name}
            badge={<StatusChip status={s.status} />}
            fields={[['Email', s.email], ['Role', s.role]]}
            actions={revoke(s)}
          />
        ))}
      </MobileOnly>
    </>
  );
}

function AssignForm({ people, onAssign }) {
  const [choice, setChoice] = useState(people[0]?.id ?? '');
  if (people.length === 0) return null;
  return (
    <Stack direction="row" sx={{ gap: 1, alignItems: 'center' }}>
      <TextField
        select
        size="small"
        value={choice}
        onChange={(e) => setChoice(e.target.value)}
        slotProps={{ select: { native: true }, htmlInput: { 'aria-label': 'Gate staff' } }}
      >
        {people.map((p) => (
          <option key={p.id} value={p.id}>{p.name}</option>
        ))}
      </TextField>
      <Button variant="outlined" size="small" onClick={() => onAssign(choice)}>Assign</Button>
    </Stack>
  );
}

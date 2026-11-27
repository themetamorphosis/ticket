'use client';

import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import Link from 'next/link';
import { useState, useSyncExternalStore } from 'react';
import { api, resetSessionCache } from '../lib/api';
import { AuthCard } from './SignIn';

// The token is read once from the URL fragment and removed from the address
// bar; later reads return the same value. The server render has no token.
let taken;
function takeToken() {
  if (taken === undefined) {
    taken = window.location.hash.slice(1);
    if (taken) window.history.replaceState(null, '', window.location.pathname);
  }
  return taken;
}

const noSubscription = () => () => {};

/** Accept an invitation: choose a password. */
export function InviteAccept({ getToken = takeToken }) {
  const token = useSyncExternalStore(noSubscription, getToken, () => null);
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);

  async function submit(e) {
    e.preventDefault();
    if (password !== confirm) {
      setError('The passwords do not match.');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.acceptInvite(token, password);
      resetSessionCache();
      setDone(true);
    } catch (err) {
      setError(err.message);
      setBusy(false);
    }
  }

  if (token === null) return <AuthCard title="Accept invitation" busy />;
  if (!token) {
    return (
      <AuthCard title="Accept invitation">
        <Alert severity="error">Open the full invitation link you were given.</Alert>
      </AuthCard>
    );
  }
  if (done) {
    return (
      <AuthCard title="You are set up">
        <Typography sx={{ mb: 2 }}>Your password is set.</Typography>
        <Button component={Link} href="/" variant="contained">
          Continue to the staff site
        </Button>
      </AuthCard>
    );
  }
  return (
    <AuthCard title="Accept invitation">
      <Typography color="text.secondary" sx={{ mb: 2 }}>
        Choose a password of at least 12 characters.
      </Typography>
      <Stack component="form" onSubmit={submit} spacing={2}>
        <TextField
          label="New password"
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
        />
        <TextField
          label="Repeat password"
          type="password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          required
        />
        {error && <Alert severity="error">{error}</Alert>}
        <Button type="submit" variant="contained" size="large" disabled={busy}>
          {busy ? 'Saving…' : 'Set password'}
        </Button>
      </Stack>
    </AuthCard>
  );
}

'use client';

import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import { useState } from 'react';
import { api, resetSessionCache } from '../lib/api';
import { PageContainer } from './PageContainer';

/**
 * The narrow, chrome-free card used by sign-in and invitation pages. These
 * have no app bar to carry a PageHeader, so they title themselves.
 */
export function AuthCard({ title, busy, children }) {
  return (
    <PageContainer maxWidth={480}>
      <Card variant="outlined" aria-busy={busy || undefined} sx={{ mt: { xs: 4, sm: 10 } }}>
        <CardContent sx={{ p: { xs: 3, sm: 4 } }}>
          <Typography variant="overline" color="text.secondary">
            Kacheem Staff
          </Typography>
          <Typography component="h1" variant="h5" sx={{ fontWeight: 600, mb: 2 }}>
            {title}
          </Typography>
          {children}
        </CardContent>
      </Card>
    </PageContainer>
  );
}

/** Email and password. */
export function SignIn({ onSignedIn }) {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);

  async function submit(e) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.login(email, password);
      resetSessionCache();
      onSignedIn();
    } catch (err) {
      setError(err.message);
      setBusy(false);
    }
  }

  return (
    <AuthCard title="Staff sign-in">
      <Stack component="form" onSubmit={submit} spacing={2}>
        <TextField label="Email" type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required />
        <TextField
          label="Password"
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
        />
        {error && <Alert severity="error">{error}</Alert>}
        <Button type="submit" variant="contained" size="large" disabled={busy}>
          {busy ? 'Signing in…' : 'Sign in'}
        </Button>
      </Stack>
    </AuthCard>
  );
}

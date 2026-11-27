'use client';

import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import DialogContentText from '@mui/material/DialogContentText';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import { useState } from 'react';
import { ConfirmDialog } from './ConfirmDialog';

/**
 * A button that asks for a reason (kept in the audit log) in a dialog before
 * running action(reason), then reports the outcome beside the button.
 */
export function ReasonAction({ label, action, onDone, needsReason = true, destructive = false }) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState('');
  const [message, setMessage] = useState(null);
  const [error, setError] = useState(null);
  const [busy, setBusy] = useState(false);

  async function run() {
    setBusy(true);
    try {
      await action(reason);
      setMessage(`${label}: done.`);
      setOpen(false);
      onDone();
    } catch (err) {
      // With a dialog open the failure belongs in it; without one, beside the button.
      (needsReason ? setError : setMessage)(err.message);
    } finally {
      setBusy(false);
    }
  }

  function start() {
    if (!needsReason) {
      run();
      return;
    }
    setError(null);
    setOpen(true);
  }

  return (
    <Stack direction="row" component="span" sx={{ display: 'inline-flex', alignItems: 'center', gap: 1, flexWrap: 'wrap' }}>
      <Button variant="outlined" size="small" onClick={start} disabled={busy}>
        {label}
      </Button>
      {message && (
        <Typography component="span" variant="body2" role="status" color="text.secondary">
          {message}
        </Typography>
      )}
      {needsReason && (
        <ConfirmDialog open={open} title={label} destructive={destructive} busy={busy} onConfirm={run} onClose={() => setOpen(false)}>
          <DialogContentText sx={{ mb: 2 }}>The reason is kept in the audit log.</DialogContentText>
          <TextField
            label={`Reason for “${label}”`}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            required
            fullWidth
            autoFocus
          />
          {error && (
            <Alert severity="error" sx={{ mt: 2 }}>
              {error}
            </Alert>
          )}
        </ConfirmDialog>
      )}
    </Stack>
  );
}

'use client';

import { useState } from 'react';

/**
 * A button that asks for a reason (kept in the audit log) before running
 * action(reason), then reports the outcome.
 */
export function ReasonAction({ label, action, onDone, needsReason = true }) {
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState('');
  const [message, setMessage] = useState(null);
  const [busy, setBusy] = useState(false);

  async function run(e) {
    e.preventDefault();
    setBusy(true);
    try {
      await action(reason);
      setMessage(`${label}: done.`);
      setOpen(false);
      onDone();
    } catch (err) {
      setMessage(err.message);
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <span className="action">
        <button type="button" className="secondary" onClick={() => (needsReason ? setOpen(true) : run({ preventDefault() {} }))}>
          {label}
        </button>
        {message && <span role="status"> {message}</span>}
      </span>
    );
  }
  return (
    <form className="action" onSubmit={run}>
      <label>
        Reason for “{label}”
        <input value={reason} onChange={(e) => setReason(e.target.value)} required />
      </label>
      <button type="submit" className="primary" disabled={busy}>Confirm</button>
      <button type="button" className="secondary" onClick={() => setOpen(false)}>Cancel</button>
      {message && <span role="status"> {message}</span>}
    </form>
  );
}

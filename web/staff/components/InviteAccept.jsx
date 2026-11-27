'use client';

import Link from 'next/link';
import { useState, useSyncExternalStore } from 'react';
import { api, resetSessionCache } from '../lib/api';
import { creationOptions, credentialJSON } from '../lib/webauthn';

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

/** Accept an invitation: choose a password and register a security key. */
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
      const { options } = await api.inviteBegin(token, password);
      const cred = await navigator.credentials.create(creationOptions(options));
      await api.inviteFinish(credentialJSON(cred));
      resetSessionCache();
      setDone(true);
    } catch (err) {
      setError(err.name === 'NotAllowedError' ? 'The security key registration was cancelled or timed out.' : err.message);
      setBusy(false);
    }
  }

  if (token === null) {
    return (
      <main className="page narrow" aria-busy="true">
        <h1>Accept invitation</h1>
      </main>
    );
  }
  if (!token) {
    return (
      <main className="page narrow">
        <h1>Accept invitation</h1>
        <p role="alert">Open the full invitation link you were given.</p>
      </main>
    );
  }
  if (done) {
    return (
      <main className="page narrow">
        <h1>You are set up</h1>
        <p>
          Your security key is registered. <Link href="/">Continue to the staff site</Link>
        </p>
      </main>
    );
  }
  return (
    <main className="page narrow">
      <h1>Accept invitation</h1>
      <p>Choose a password of at least 12 characters, then register your security key or passkey on this device.</p>
      <form onSubmit={submit} className="stack">
        <label>
          New password
          <input type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        </label>
        <label>
          Repeat password
          <input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
        </label>
        {error && <p role="alert">{error}</p>}
        <button type="submit" className="primary" disabled={busy}>
          {busy ? 'Waiting for your security key…' : 'Register security key'}
        </button>
      </form>
    </main>
  );
}

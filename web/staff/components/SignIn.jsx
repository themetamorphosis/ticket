'use client';

import { useState } from 'react';
import { api, resetSessionCache } from '../lib/api';
import { credentialJSON, requestOptions } from '../lib/webauthn';

/** Password, then the security key (WebAuthn). */
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
      const { options } = await api.loginBegin(email, password);
      const cred = await navigator.credentials.get(requestOptions(options));
      await api.loginFinish(credentialJSON(cred));
      resetSessionCache();
      onSignedIn();
    } catch (err) {
      setError(err.name === 'NotAllowedError' ? 'The security key check was cancelled or timed out.' : err.message);
      setBusy(false);
    }
  }

  return (
    <main className="page narrow">
      <h1>Staff sign-in</h1>
      <form onSubmit={submit} className="stack">
        <label>
          Email
          <input type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required />
        </label>
        <label>
          Password
          <input
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </label>
        {error && <p role="alert">{error}</p>}
        <button type="submit" className="primary" disabled={busy}>
          {busy ? 'Waiting for your security key…' : 'Sign in'}
        </button>
      </form>
    </main>
  );
}

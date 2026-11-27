'use client';

import Link from 'next/link';
import { useCallback, useEffect, useState } from 'react';
import { api, resetSessionCache } from '../lib/api';
import { SignIn } from './SignIn';

const PAGES = [
  { href: '/', label: 'Overview', roles: ['organizer', 'finance', 'support', 'gate'] },
  { href: '/scan/', label: 'Scanner', roles: ['organizer', 'gate'] },
  { href: '/orders/', label: 'Orders', roles: ['organizer', 'finance', 'support'] },
  { href: '/finance/', label: 'Finance', roles: ['organizer', 'finance'] },
  { href: '/event/', label: 'Event', roles: ['organizer'] },
  { href: '/people/', label: 'Gates and staff', roles: ['organizer'] },
  { href: '/audit/', label: 'Audit log', roles: ['organizer'] },
];

/**
 * Loads the signed-in staff member and renders children(me) when their role
 * may use this page, a sign-in form when signed out, or a refusal otherwise.
 */
export function StaffShell({ roles, children }) {
  const [me, setMe] = useState(null);
  const [state, setState] = useState('loading');

  const load = useCallback(() => {
    api
      .me()
      .then((m) => {
        setMe(m);
        setState('ready');
      })
      .catch((err) => setState(err.code === 'signed_out' ? 'signed_out' : 'error'));
  }, []);
  useEffect(load, [load]);

  async function signOut() {
    await api.logout().catch(() => {});
    resetSessionCache();
    setMe(null);
    setState('signed_out');
  }

  if (state === 'loading') return <main className="page" aria-busy="true"><p>Loading…</p></main>;
  if (state === 'error') return <main className="page"><p role="alert">The staff service is unavailable. Check the VPN and refresh.</p></main>;
  if (state === 'signed_out') return <SignIn onSignedIn={load} />;

  const role = me.staff.role;
  return (
    <>
      <header className="topbar">
        <nav aria-label="Staff">
          {PAGES.filter((p) => p.roles.includes(role)).map((p) => (
            <Link key={p.href} href={p.href}>
              {p.label}
            </Link>
          ))}
        </nav>
        <p className="who">
          {me.staff.name} · {role}{' '}
          <button type="button" className="link" onClick={signOut}>
            Sign out
          </button>
        </p>
      </header>
      <main className="page">
        {roles.includes(role) ? children(me) : <p role="alert">Your role cannot use this page.</p>}
      </main>
    </>
  );
}

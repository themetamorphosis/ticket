'use client';

import { useCallback, useState } from 'react';
import { api } from '../lib/api';
import { ReasonAction } from './ReasonAction';
import { useLoad } from './useLoad';

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

  if (gates.error || staff.error) return <p role="alert">{gates.error ?? staff.error}</p>;
  if (!gates.data || !staff.data) return <p aria-busy="true">Loading…</p>;
  const gateStaff = staff.data.staff.filter((s) => s.role === 'gate' && s.status === 'active');
  return (
    <>
      <h1>Gates and staff</h1>
      {message && <p role="alert">{message}</p>}
      <h2>Gates</h2>
      <ul>
        {gates.data.gates.map((g) => (
          <li key={g.id}>
            <strong>{g.name}</strong>: {g.staff.length ? g.staff.join(', ') : 'nobody assigned'}{' '}
            <AssignForm people={gateStaff} onAssign={(id) => api.assign(g.id, id).then(gates.reload, (err) => setMessage(err.message))} />
          </li>
        ))}
      </ul>
      <form className="row" onSubmit={run(() => api.addGate(slug, gateName), () => { setGateName(''); gates.reload(); })}>
        <label>
          New gate
          <input value={gateName} onChange={(e) => setGateName(e.target.value)} required />
        </label>
        <button type="submit" className="secondary">Add gate</button>
      </form>

      <h2>Staff</h2>
      <table>
        <thead><tr><th>Name</th><th>Email</th><th>Role</th><th>Status</th><th></th></tr></thead>
        <tbody>
          {staff.data.staff.map((s) => (
            <tr key={s.id}>
              <td>{s.name}</td><td>{s.email}</td><td>{s.role}</td><td>{s.status}</td>
              <td>
                {s.status !== 'disabled' && s.id !== me.staff.id && (
                  <ReasonAction label="Revoke access" action={(r) => api.disable(s.id, r)} onDone={staff.reload} />
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <form className="stack" onSubmit={run(() => api.invite(invite.email, invite.name, invite.role), (res) => { setInviteURL(res.invite_url); staff.reload(); })}>
        <h3>Invite someone</h3>
        <label>Name<input value={invite.name} onChange={(e) => setInvite({ ...invite, name: e.target.value })} required /></label>
        <label>Email<input type="email" value={invite.email} onChange={(e) => setInvite({ ...invite, email: e.target.value })} required /></label>
        <label>
          Role
          <select value={invite.role} onChange={(e) => setInvite({ ...invite, role: e.target.value })}>
            <option value="gate">Gate</option>
            <option value="support">Support</option>
            <option value="finance">Finance</option>
            <option value="organizer">Organizer</option>
          </select>
        </label>
        <button type="submit" className="primary">Create invitation</button>
      </form>
      {inviteURL && (
        <p role="status">
          Send this one-time link to them privately (valid 72 hours): <code>{inviteURL}</code>
        </p>
      )}
    </>
  );
}

function AssignForm({ people, onAssign }) {
  const [choice, setChoice] = useState(people[0]?.id ?? '');
  if (people.length === 0) return null;
  return (
    <>
      <select aria-label="Gate staff" value={choice} onChange={(e) => setChoice(e.target.value)}>
        {people.map((p) => (
          <option key={p.id} value={p.id}>{p.name}</option>
        ))}
      </select>
      <button type="button" className="secondary" onClick={() => onAssign(choice)}>Assign</button>
    </>
  );
}

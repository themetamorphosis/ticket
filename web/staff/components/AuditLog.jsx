'use client';

import { useCallback } from 'react';
import { api } from '../lib/api';
import { when } from '../lib/format';
import { useLoad } from './useLoad';

export function AuditLog() {
  const load = useCallback(() => api.audit(), []);
  const { data, error } = useLoad(load);
  if (error) return <p role="alert">{error}</p>;
  if (!data) return <p aria-busy="true">Loading…</p>;
  return (
    <>
      <h1>Audit log</h1>
      <table>
        <thead><tr><th>When</th><th>Who</th><th>Action</th><th>Object</th><th>Reason</th></tr></thead>
        <tbody>
          {data.entries.map((e) => (
            <tr key={`${e.at}-${e.action}-${e.object_id}`}>
              <td>{when(e.at)}</td><td>{e.actor}</td><td>{e.action}</td><td>{e.object_kind} {e.object_id.slice(0, 8)}</td><td>{e.reason}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}

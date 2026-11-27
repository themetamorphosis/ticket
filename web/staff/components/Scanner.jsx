'use client';

import { Alert02Icon, CancelCircleIcon, CheckmarkCircle02Icon, WifiDisconnected01Icon } from '@hugeicons/core-free-icons';
import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../lib/api';
import { deviceId } from '../lib/device';
import { beep } from '../lib/sound';
import { Camera } from './Camera';
import { Icon } from './Icon';

// A ticket QR opened directly (the phone camera opens /scan#<credential>).
// It is only offered for confirmation: opening a link never admits anyone.
function takeHashScan() {
  const hash = window.location.hash.slice(1);
  if (!hash) return null;
  window.history.replaceState(null, '', window.location.pathname);
  return `${window.location.origin}/scan#${hash}`;
}

const LOOK = {
  admitted: { tone: 'ok', icon: CheckmarkCircle02Icon },
  already_used: { tone: 'warn', icon: Alert02Icon },
};

function Result({ result }) {
  const look = LOOK[result.outcome] ?? { tone: 'stop', icon: CancelCircleIcon };
  const time = (iso) => new Date(iso).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  return (
    <div className={`result ${look.tone}`} role="status">
      <Icon icon={look.icon} size={64} />
      <p className="verdict">{result.replay ? 'Previous scan confirmed' : result.message}</p>
      {result.ticket && <p>{result.ticket}</p>}
      {result.first_gate && (
        <p>
          First admitted at {result.first_gate}, {time(result.first_at)}. Send the holder to the supervisor.
        </p>
      )}
      {result.replay && <p>{result.message} at {time(result.decided_at)}</p>}
    </div>
  );
}

/**
 * The gate scanner. The operator arms one scan with "Admit next"; the first
 * code read is submitted once and the camera stops until the result is
 * acknowledged. A lost answer is retried with the same request ID, so it can
 * never admit twice; the screen never shows green without the server.
 */
export function Scanner({ me, makeRequestId = () => crypto.randomUUID(), readHash = takeHashScan, CameraView = Camera }) {
  const slug = me.events[0]?.slug;
  const [gates, setGates] = useState(null);
  const [gate, setGate] = useState('');
  const [phase, setPhase] = useState('idle');
  const [result, setResult] = useState(null);
  const [code, setCode] = useState('');
  const [linked, setLinked] = useState(readHash);
  const last = useRef(null);

  useEffect(() => {
    if (!slug) return;
    api
      .myGates(slug)
      .then((res) => {
        setGates(res.gates);
        setGate(res.gates[0]?.id ?? '');
      })
      .catch(() => setGates([]));
  }, [slug]);

  const send = useCallback((req) => {
    setPhase('deciding');
    api
      .admit(req)
      .then((res) => {
        setResult(res);
        setPhase('result');
        beep(res.outcome === 'admitted');
      })
      .catch((err) => {
        if (err.code === 'network' || err.status >= 500) {
          setPhase('unconfirmed');
          return;
        }
        setResult({ outcome: err.code, message: err.message });
        setPhase('result');
        beep(false);
      });
  }, []);

  const submit = useCallback(
    (scan) => {
      last.current = { gate_id: gate, device_id: deviceId(), request_id: makeRequestId(), ...scan };
      send(last.current);
    },
    [gate, makeRequestId, send],
  );

  const onCode = useCallback(
    (text) => {
      if (phase === 'armed') submit({ qr: text });
    },
    [phase, submit],
  );

  if (!slug) return <p>There is no event to scan for.</p>;
  if (gates === null) return <p aria-busy="true">Loading your gates…</p>;
  if (gates.length === 0) return <p role="alert">You are not assigned to a gate. Ask the organizer.</p>;

  return (
    <section className="scanner" aria-labelledby="scanner-title">
      <h1 id="scanner-title">Scanner</h1>
      <label>
        Gate
        <select value={gate} onChange={(e) => setGate(e.target.value)} disabled={phase !== 'idle'}>
          {gates.map((g) => (
            <option key={g.id} value={g.id}>
              {g.name}
            </option>
          ))}
        </select>
      </label>

      {linked && phase === 'idle' && (
        <div className="linked">
          <p>A ticket link was opened on this device.</p>
          <button type="button" className="primary" onClick={() => { setLinked(null); submit({ qr: linked }); }}>
            Admit this ticket
          </button>
          <button type="button" className="secondary" onClick={() => setLinked(null)}>
            Dismiss
          </button>
        </div>
      )}

      {phase === 'idle' && (
        <button type="button" className="primary big" onClick={() => setPhase('armed')}>
          Admit next
        </button>
      )}
      <CameraView active={phase === 'armed'} onCode={onCode} />
      {phase === 'armed' && (
        <button type="button" className="secondary" onClick={() => setPhase('idle')}>
          Cancel scan
        </button>
      )}
      {phase === 'deciding' && <p aria-live="polite">Checking…</p>}
      {phase === 'unconfirmed' && (
        <div className="result unconfirmed" role="status">
          <Icon icon={WifiDisconnected01Icon} size={64} />
          <p className="verdict">Unconfirmed</p>
          <p>No answer from the server. Do not admit yet. Retry, move to the backup network, or send the holder to the supervisor desk.</p>
          <button type="button" className="primary" onClick={() => send(last.current)}>
            Retry the same scan
          </button>
          <button type="button" className="secondary" onClick={() => setPhase('idle')}>
            Give up
          </button>
        </div>
      )}
      {phase === 'result' && (
        <>
          <Result result={result} />
          <button type="button" className="primary big" onClick={() => setPhase('idle')}>
            Next person
          </button>
        </>
      )}

      {phase === 'idle' && (
        <form
          className="stack"
          onSubmit={(e) => {
            e.preventDefault();
            submit({ code });
            setCode('');
          }}
        >
          <label>
            Or type the code printed on the ticket
            <input value={code} onChange={(e) => setCode(e.target.value)} autoCapitalize="characters" autoComplete="off" required />
          </label>
          <button type="submit" className="secondary">
            Check code
          </button>
        </form>
      )}
    </section>
  );
}

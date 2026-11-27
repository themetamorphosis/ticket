'use client';

import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Paper from '@mui/material/Paper';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from '../lib/api';
import { deviceId } from '../lib/device';
import { beep } from '../lib/sound';
import { Camera } from './Camera';
import { Cancel as CancelIcon, CheckCircle as CheckCircleIcon, Warning as WarningIcon, WifiOff as WifiOffIcon } from './icons';
import { PageContainer } from './PageContainer';
import { PageHeader } from './PageHeader';
import { EmptyState, LoadingState } from './States';

// A ticket QR opened directly (the phone camera opens /scan#<credential>).
// It is only offered for confirmation: opening a link never admits anyone.
function takeHashScan() {
  const hash = window.location.hash.slice(1);
  if (!hash) return null;
  window.history.replaceState(null, '', window.location.pathname);
  return `${window.location.origin}/scan#${hash}`;
}

// The verdict is carried by colour, icon, words and sound together.
const LOOK = {
  admitted: { tone: 'success', icon: CheckCircleIcon },
  already_used: { tone: 'warning', icon: WarningIcon },
};

/** A full-width verdict panel in a semantic colour. */
function Verdict({ bgcolor, color, icon: VerdictIcon, title, children }) {
  return (
    <Paper role="status" elevation={0} sx={{ bgcolor, color, p: 3, borderRadius: 4, textAlign: 'center', display: 'grid', justifyItems: 'center', gap: 0.5 }}>
      <VerdictIcon size={64} />
      <Typography component="p" variant="h4" sx={{ fontWeight: 800 }}>{title}</Typography>
      {children}
    </Paper>
  );
}

function Result({ result }) {
  const look = LOOK[result.outcome] ?? { tone: 'error', icon: CancelIcon };
  const time = (iso) => new Date(iso).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  return (
    <Verdict
      bgcolor={`${look.tone}.main`}
      color={`${look.tone}.contrastText`}
      icon={look.icon}
      title={result.replay ? 'Previous scan confirmed' : result.message}
    >
      {result.ticket && <Typography>{result.ticket}</Typography>}
      {result.first_gate && (
        <Typography>
          First admitted at {result.first_gate}, {time(result.first_at)}. Send the holder to the supervisor.
        </Typography>
      )}
      {result.replay && <Typography>{result.message} at {time(result.decided_at)}</Typography>}
    </Verdict>
  );
}

const big = { width: '100%', minHeight: 64, fontSize: '1.25rem' };

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

  let body;
  if (!slug) body = <EmptyState title="There is no event to scan for." />;
  else if (gates === null) body = <LoadingState label="Loading your gates…" />;
  else if (gates.length === 0) body = <Alert severity="warning">You are not assigned to a gate. Ask the organizer.</Alert>;
  else {
    body = (
      <Stack spacing={2} sx={{ maxWidth: 560 }}>
        <TextField
          select
          label="Gate"
          value={gate}
          onChange={(e) => setGate(e.target.value)}
          disabled={phase !== 'idle'}
          slotProps={{ select: { native: true } }}
        >
          {gates.map((g) => (
            <option key={g.id} value={g.id}>{g.name}</option>
          ))}
        </TextField>

        {linked && phase === 'idle' && (
          <Paper variant="outlined" sx={{ p: 2, borderStyle: 'dashed', borderColor: 'primary.main' }}>
            <Typography sx={{ mb: 1.5 }}>A ticket link was opened on this device.</Typography>
            <Stack direction="row" sx={{ gap: 1, flexWrap: 'wrap' }}>
              <Button variant="contained" onClick={() => { setLinked(null); submit({ qr: linked }); }}>
                Admit this ticket
              </Button>
              <Button onClick={() => setLinked(null)}>Dismiss</Button>
            </Stack>
          </Paper>
        )}

        {phase === 'idle' && (
          <Button variant="contained" size="large" sx={big} onClick={() => setPhase('armed')}>
            Admit next
          </Button>
        )}
        <CameraView active={phase === 'armed'} onCode={onCode} />
        {phase === 'armed' && (
          <Button variant="outlined" onClick={() => setPhase('idle')}>
            Cancel scan
          </Button>
        )}
        {phase === 'deciding' && <LoadingState label="Checking…" />}
        {phase === 'unconfirmed' && (
          <Verdict bgcolor="grey.800" color="common.white" icon={WifiOffIcon} title="Unconfirmed">
            <Typography>
              No answer from the server. Do not admit yet. Retry, move to the backup network, or send the holder to the supervisor desk.
            </Typography>
            <Stack direction="row" sx={{ gap: 1, flexWrap: 'wrap', justifyContent: 'center', mt: 1.5 }}>
              <Button variant="contained" onClick={() => send(last.current)}>
                Retry the same scan
              </Button>
              <Button color="inherit" onClick={() => setPhase('idle')}>
                Give up
              </Button>
            </Stack>
          </Verdict>
        )}
        {phase === 'result' && (
          <>
            <Result result={result} />
            <Button variant="contained" size="large" sx={big} onClick={() => setPhase('idle')}>
              Next person
            </Button>
          </>
        )}

        {phase === 'idle' && (
          <Stack
            component="form"
            direction="row"
            sx={{ gap: 1.5, alignItems: 'center' }}
            onSubmit={(e) => {
              e.preventDefault();
              submit({ code });
              setCode('');
            }}
          >
            <TextField
              label="Or type the code printed on the ticket"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              required
              fullWidth
              slotProps={{ htmlInput: { autoCapitalize: 'characters', autoComplete: 'off' } }}
            />
            <Button type="submit" variant="outlined" sx={{ flexShrink: 0 }}>
              Check code
            </Button>
          </Stack>
        )}
      </Stack>
    );
  }

  return (
    <PageContainer>
      <PageHeader title="Scanner" />
      {body}
    </PageContainer>
  );
}

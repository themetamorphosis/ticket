import { screen } from '@testing-library/react';
import { render } from '../test/render';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from '../lib/api';
import { beep } from '../lib/sound';
import { me } from '../test/apiMock';
import { Scanner } from './Scanner';

vi.mock('../lib/api', async (importOriginal) => (await import('../test/apiMock')).apiMock(await importOriginal()));
vi.mock('../lib/sound', () => ({ beep: vi.fn() }));
vi.mock('../lib/device', () => ({ deviceId: () => 'dev-test-device' }));

// A camera stand-in: shows whether it is on and lets the test "read" a code.
function FakeCamera({ active, onCode }) {
  return (
    <div>
      <p>camera {active ? 'on' : 'off'}</p>
      <button type="button" onClick={() => onCode('https://gate.test/scan#cred')}>read QR</button>
    </div>
  );
}

const gates = { gates: [{ id: 'g1', name: 'North' }, { id: 'g2', name: 'South' }] };
let n;
const ids = () => `req-${++n}`;

function renderScanner(props = {}) {
  return render(<Scanner me={me('gate')} makeRequestId={ids} readHash={() => null} CameraView={FakeCamera} {...props} />);
}

beforeEach(() => {
  n = 0;
  Object.values(api).forEach((f) => f.mockReset());
  beep.mockReset();
  api.myGates.mockResolvedValue(gates);
});

describe('Scanner', () => {
  it('arms one scan, admits, and waits for the next person', async () => {
    const user = userEvent.setup();
    api.admit.mockResolvedValue({ outcome: 'admitted', message: 'Admit', replay: false, ticket: 'General admission · ticket 1 of 3' });
    renderScanner();
    expect(screen.getByText('Loading your gates…')).toBeInTheDocument();
    await user.click(await screen.findByRole('button', { name: 'read QR' }));
    expect(api.admit).not.toHaveBeenCalled(); // not armed
    expect(screen.getByText('camera off')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Admit next' }));
    expect(screen.getByText('camera on')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'read QR' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Admit');
    expect(screen.getByRole('status')).toHaveTextContent('General admission · ticket 1 of 3');
    expect(screen.getByText('camera off')).toBeInTheDocument();
    expect(api.admit).toHaveBeenCalledWith({ gate_id: 'g1', device_id: 'dev-test-device', request_id: 'req-1', qr: 'https://gate.test/scan#cred' });
    expect(beep).toHaveBeenCalledWith(true);
    await user.click(screen.getByRole('button', { name: 'Next person' }));
    expect(screen.getByRole('button', { name: 'Admit next' })).toBeInTheDocument();
  });

  it('shows where and when a used ticket was admitted, and confirms a repeat', async () => {
    const user = userEvent.setup();
    api.admit
      .mockResolvedValueOnce({ outcome: 'already_used', message: 'Already used', replay: false, ticket: 'GA · 1 of 1',
        first_gate: 'North', first_at: '2027-02-14T12:31:05Z' })
      .mockResolvedValueOnce({ outcome: 'admitted', message: 'Admit', replay: true, ticket: '', decided_at: '2027-02-14T12:31:05Z',
        first_gate: 'North', first_at: '2027-02-14T12:31:05Z' });
    renderScanner();
    await user.click(await screen.findByRole('button', { name: 'Admit next' }));
    await user.click(screen.getByRole('button', { name: 'read QR' }));
    expect(await screen.findByRole('status')).toHaveTextContent(/Already used.*First admitted at North, \d\d:\d\d:\d\d.*supervisor/);
    expect(beep).toHaveBeenCalledWith(false);
    await user.click(screen.getByRole('button', { name: 'Next person' }));
    await user.click(screen.getByRole('button', { name: 'Admit next' }));
    await user.click(screen.getByRole('button', { name: 'read QR' }));
    expect(await screen.findByRole('status')).toHaveTextContent(/Previous scan confirmed.*Admit at \d\d:\d\d/);
  });

  it('never shows green without the server: an unanswered scan is retried as the same request', async () => {
    const user = userEvent.setup();
    api.admit
      .mockRejectedValueOnce(new ApiError(0, 'network', 'offline'))
      .mockRejectedValueOnce(new ApiError(502, 'unknown', 'bad gateway'))
      .mockResolvedValueOnce({ outcome: 'admitted', message: 'Admit', replay: true, ticket: 'GA', decided_at: '2027-02-14T12:31:05Z' });
    renderScanner();
    await user.click(await screen.findByRole('button', { name: 'Admit next' }));
    await user.click(screen.getByRole('button', { name: 'read QR' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Unconfirmed');
    await user.click(screen.getByRole('button', { name: 'Retry the same scan' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Unconfirmed');
    await user.click(screen.getByRole('button', { name: 'Retry the same scan' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Previous scan confirmed');
    const requestIds = api.admit.mock.calls.map((c) => c[0].request_id);
    expect(new Set(requestIds)).toEqual(new Set(['req-1']));
  });

  it('can give up on an unconfirmed scan and cancel an armed one', async () => {
    const user = userEvent.setup();
    api.admit.mockRejectedValue(new ApiError(0, 'network', 'offline'));
    renderScanner();
    await user.click(await screen.findByRole('button', { name: 'Admit next' }));
    await user.click(screen.getByRole('button', { name: 'Cancel scan' }));
    expect(screen.getByText('camera off')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Admit next' }));
    await user.click(screen.getByRole('button', { name: 'read QR' }));
    await user.click(await screen.findByRole('button', { name: 'Give up' }));
    expect(screen.getByRole('button', { name: 'Admit next' })).toBeInTheDocument();
  });

  it('checks typed codes and reports codes that are not tickets', async () => {
    const user = userEvent.setup();
    api.admit.mockRejectedValue(new ApiError(422, 'bad_scan', 'That is not a ticket code.'));
    renderScanner();
    await user.selectOptions(await screen.findByLabelText('Gate'), 'g2');
    await user.type(screen.getByLabelText(/type the code/), 'ABCD-EFGH');
    await user.click(screen.getByRole('button', { name: 'Check code' }));
    expect(await screen.findByRole('status')).toHaveTextContent('That is not a ticket code.');
    expect(api.admit).toHaveBeenCalledWith(expect.objectContaining({ gate_id: 'g2', code: 'ABCD-EFGH' }));
  });

  it('offers a ticket link opened on the device for explicit admission', async () => {
    const user = userEvent.setup();
    api.admit.mockResolvedValue({ outcome: 'void', message: 'Do not admit: ticket cancelled or refunded', replay: false, ticket: 'GA' });
    renderScanner({ readHash: () => 'https://gate.test/scan#linked' });
    await user.click(await screen.findByRole('button', { name: 'Admit this ticket' }));
    expect(await screen.findByRole('status')).toHaveTextContent('Do not admit');
    expect(api.admit).toHaveBeenCalledWith(expect.objectContaining({ qr: 'https://gate.test/scan#linked' }));

    renderScanner({ readHash: () => 'https://gate.test/scan#other' });
    const dismiss = await screen.findByRole('button', { name: 'Dismiss' });
    await user.click(dismiss);
    expect(screen.queryByRole('button', { name: 'Dismiss' })).not.toBeInTheDocument();
  });

  it('reads a ticket link from the address bar by default', async () => {
    window.history.pushState({}, '', '/scan/#fromlink');
    render(<Scanner me={me('gate')} CameraView={FakeCamera} />);
    expect(await screen.findByRole('button', { name: 'Admit this ticket' })).toBeInTheDocument();
    expect(window.location.hash).toBe('');
    // Without a fragment nothing is offered.
    window.history.pushState({}, '', '/scan/');
    render(<Scanner me={me('gate')} CameraView={FakeCamera} />);
    await screen.findAllByText('camera off');
    expect(screen.getAllByRole('button', { name: 'Admit this ticket' })).toHaveLength(1);
  });

  it('makes a fresh request ID per scan by default', async () => {
    const user = userEvent.setup();
    api.admit.mockResolvedValue({ outcome: 'unknown', message: 'Do not admit: unknown ticket', replay: false, ticket: '' });
    render(<Scanner me={me('gate')} readHash={() => null} CameraView={FakeCamera} />);
    await user.click(await screen.findByRole('button', { name: 'Admit next' }));
    await user.click(screen.getByRole('button', { name: 'read QR' }));
    await screen.findByRole('status');
    expect(api.admit.mock.calls[0][0].request_id).toMatch(/^[0-9a-f-]{36}$/);
  });

  it.each([
    ['no event', { ...me('gate'), events: [] }, null, 'There is no event to scan for.'],
    ['no gates', me('gate'), { gates: [] }, 'You are not assigned to a gate.'],
    ['gates unavailable', me('gate'), new ApiError(0, 'network', 'x'), 'You are not assigned to a gate.'],
  ])('explains %s', async (_name, who, gatesReply, text) => {
    if (gatesReply instanceof Error) api.myGates.mockRejectedValue(gatesReply);
    else if (gatesReply) api.myGates.mockResolvedValue(gatesReply);
    render(<Scanner me={who} readHash={() => null} CameraView={FakeCamera} />);
    expect(await screen.findByText(new RegExp(text))).toBeInTheDocument();
  });
});

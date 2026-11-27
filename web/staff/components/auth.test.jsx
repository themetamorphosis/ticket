import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderToString } from 'react-dom/server';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api, resetSessionCache } from '../lib/api';
import { me } from '../test/apiMock';
import { InviteAccept } from './InviteAccept';
import { SignIn } from './SignIn';
import { StaffShell } from './StaffShell';

vi.mock('../lib/api', async (importOriginal) => (await import('../test/apiMock')).apiMock(await importOriginal()));
vi.mock('next/navigation', () => ({ useRouter: () => ({ push: vi.fn() }) }));

const cred = { id: 'c', rawId: new Uint8Array([1]).buffer, type: 'public-key',
  response: { clientDataJSON: new Uint8Array([2]).buffer, authenticatorData: new Uint8Array([3]).buffer, signature: new Uint8Array([4]).buffer, userHandle: null } };
const attestation = { id: 'c', rawId: new Uint8Array([1]).buffer, type: 'public-key',
  response: { clientDataJSON: new Uint8Array([2]).buffer, attestationObject: new Uint8Array([3]).buffer } };
const options = { publicKey: { challenge: 'AQI', user: { id: 'dXNlcg', name: 'a' }, allowCredentials: [] } };

beforeEach(() => {
  Object.values(api).forEach((f) => f.mockReset());
  resetSessionCache.mockReset();
  Object.defineProperty(navigator, 'credentials', { configurable: true, value: { get: vi.fn(), create: vi.fn() } });
});

describe('StaffShell', () => {
  it('shows the page and the navigation the role may use', async () => {
    api.me.mockResolvedValue(me('support'));
    render(<StaffShell roles={['support']}>{(m) => <p>Hello {m.staff.name}</p>}</StaffShell>);
    expect(screen.getByText('Loading…')).toBeInTheDocument();
    expect(await screen.findByText('Hello Staff One')).toBeInTheDocument();
    const links = screen.getAllByRole('link').map((a) => a.textContent);
    expect(links).toEqual(['Overview', 'Orders']);
  });

  it('refuses a page outside the role', async () => {
    api.me.mockResolvedValue(me('gate'));
    render(<StaffShell roles={['organizer']}>{() => <p>secret</p>}</StaffShell>);
    expect(await screen.findByRole('alert')).toHaveTextContent('Your role cannot use this page.');
    expect(screen.queryByText('secret')).not.toBeInTheDocument();
  });

  it('asks signed-out staff to sign in, and signs out', async () => {
    const user = userEvent.setup();
    api.me.mockRejectedValueOnce(new ApiError(401, 'signed_out', 'Please sign in.'));
    render(<StaffShell roles={['gate']}>{() => <p>in</p>}</StaffShell>);
    expect(await screen.findByRole('heading', { name: 'Staff sign-in' })).toBeInTheDocument();

    api.me.mockResolvedValue(me('gate'));
    api.logout.mockRejectedValue(new ApiError(0, 'network', 'x'));
    render(<StaffShell roles={['gate']}>{() => <p>in</p>}</StaffShell>);
    await user.click(await screen.findByRole('button', { name: 'Sign out' }));
    expect(screen.getAllByRole('heading', { name: 'Staff sign-in' })).toHaveLength(2);
    expect(resetSessionCache).toHaveBeenCalled();
  });

  it('reports an unreachable service', async () => {
    api.me.mockRejectedValue(new ApiError(0, 'network', 'x'));
    render(<StaffShell roles={['gate']}>{() => null}</StaffShell>);
    expect(await screen.findByRole('alert')).toHaveTextContent('Check the VPN');
  });
});

describe('SignIn', () => {
  async function fill(user) {
    await user.type(screen.getByLabelText('Email'), 'org@example.org');
    await user.type(screen.getByLabelText('Password'), 'correct horse');
    await user.click(screen.getByRole('button', { name: 'Sign in' }));
  }

  it('checks the password, then the security key', async () => {
    const user = userEvent.setup();
    const onSignedIn = vi.fn();
    api.loginBegin.mockResolvedValue({ options });
    navigator.credentials.get.mockResolvedValue(cred);
    api.loginFinish.mockResolvedValue({ staff: {} });
    render(<SignIn onSignedIn={onSignedIn} />);
    await fill(user);
    expect(onSignedIn).toHaveBeenCalled();
    expect(api.loginBegin).toHaveBeenCalledWith('org@example.org', 'correct horse');
    expect(JSON.parse(api.loginFinish.mock.calls[0][0]).rawId).toBe('AQ');
    expect(resetSessionCache).toHaveBeenCalled();
  });

  it.each([
    [Object.assign(new Error('x'), { name: 'NotAllowedError' }), 'cancelled or timed out'],
    [new ApiError(401, 'sign_in_failed', 'Those details did not match.'), 'Those details did not match.'],
  ])('explains a failure (%#)', async (err, text) => {
    const user = userEvent.setup();
    api.loginBegin.mockResolvedValue({ options });
    navigator.credentials.get.mockRejectedValue(err);
    render(<SignIn onSignedIn={vi.fn()} />);
    await fill(user);
    expect(await screen.findByRole('alert')).toHaveTextContent(text);
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeEnabled();
  });
});

describe('InviteAccept', () => {
  async function fill(user, a = 'a long password', b = a) {
    await user.type(screen.getByLabelText('New password'), a);
    await user.type(screen.getByLabelText('Repeat password'), b);
    await user.click(screen.getByRole('button', { name: 'Register security key' }));
  }

  it('sets the password and registers a key', async () => {
    const user = userEvent.setup();
    api.inviteBegin.mockResolvedValue({ options: { publicKey: { challenge: 'AQI', user: { id: 'AA' } } } });
    navigator.credentials.create.mockResolvedValue(attestation);
    api.inviteFinish.mockResolvedValue({ staff: {} });
    render(<InviteAccept getToken={() => 'tok'} />);
    await fill(user);
    expect(await screen.findByRole('heading', { name: 'You are set up' })).toBeInTheDocument();
    expect(api.inviteBegin).toHaveBeenCalledWith('tok', 'a long password');
  });

  it('checks the passwords match', async () => {
    const user = userEvent.setup();
    render(<InviteAccept getToken={() => 'tok'} />);
    await fill(user, 'a long password', 'another one!!');
    expect(screen.getByRole('alert')).toHaveTextContent('do not match');
    expect(api.inviteBegin).not.toHaveBeenCalled();
  });

  it.each([
    [Object.assign(new Error('x'), { name: 'NotAllowedError' }), 'cancelled or timed out'],
    [new ApiError(404, 'invite_invalid', 'This invitation is not valid.'), 'This invitation is not valid.'],
  ])('explains a failure (%#)', async (err, text) => {
    const user = userEvent.setup();
    api.inviteBegin.mockResolvedValue({ options: { publicKey: { challenge: 'AQI', user: { id: 'AA' } } } });
    navigator.credentials.create.mockRejectedValue(err);
    render(<InviteAccept getToken={() => 'tok'} />);
    await fill(user);
    expect(await screen.findByRole('alert')).toHaveTextContent(text);
  });

  it('reads the token from the link once and strips it', () => {
    window.history.pushState({}, '', '/invite/#secret-token');
    render(<InviteAccept />);
    expect(window.location.hash).toBe('');
    expect(screen.getByRole('button', { name: 'Register security key' })).toBeInTheDocument();
  });

  it('needs the full link, and renders empty on the server', () => {
    render(<InviteAccept getToken={() => ''} />);
    expect(screen.getByRole('alert')).toHaveTextContent('Open the full invitation link');
    expect(renderToString(<InviteAccept getToken={() => 'tok'} />)).toContain('aria-busy="true"');
  });
});

describe('InviteAccept without a token in the link', () => {
  it('asks for the full link', async () => {
    vi.resetModules();
    window.history.pushState({}, '', '/invite/');
    const { InviteAccept: Fresh } = await import('./InviteAccept');
    render(<Fresh />);
    expect(screen.getByRole('alert')).toHaveTextContent('Open the full invitation link');
  });
});

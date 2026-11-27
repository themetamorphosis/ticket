import { describe, expect, it } from 'vitest';
import { creationOptions, credentialJSON, fromB64url, requestOptions, toB64url } from './webauthn';

const bytes = (b) => new Uint8Array(b);

describe('webauthn conversions', () => {
  it('round-trips base64url of every padding length', () => {
    for (const s of ['', 'AA', 'AAE', 'AAEC', '_-8']) {
      expect(toB64url(fromB64url(s))).toBe(s);
    }
    expect(Array.from(bytes(fromB64url('_-8')))).toEqual([255, 239]);
  });

  it('turns server options into browser options', () => {
    const c = creationOptions({ publicKey: { challenge: 'AQI', rp: { id: 'gate.test' }, user: { id: 'dXNlcg', name: 'a' } } });
    expect(Array.from(bytes(c.publicKey.challenge))).toEqual([1, 2]);
    expect(new TextDecoder().decode(c.publicKey.user.id)).toBe('user');
    expect(c.publicKey.excludeCredentials).toEqual([]);
    const withExcluded = creationOptions({ publicKey: { challenge: 'AQI', user: { id: 'AA' }, excludeCredentials: [{ id: 'AQ', type: 'public-key' }] } });
    expect(Array.from(bytes(withExcluded.publicKey.excludeCredentials[0].id))).toEqual([1]);
    const r = requestOptions({ publicKey: { challenge: 'AQI', allowCredentials: [{ id: 'AQ', type: 'public-key' }] } });
    expect(Array.from(bytes(r.publicKey.allowCredentials[0].id))).toEqual([1]);
    expect(requestOptions({ publicKey: { challenge: 'AQI' } }).publicKey.allowCredentials).toEqual([]);
  });

  it('serializes attestations and assertions', () => {
    const buf = (n) => new Uint8Array([n]).buffer;
    const attestation = JSON.parse(
      credentialJSON({
        id: 'id1', rawId: buf(1), type: 'public-key',
        response: { clientDataJSON: buf(2), attestationObject: buf(3), getTransports: () => ['usb'] },
        getClientExtensionResults: () => ({ credProps: {} }),
      }),
    );
    expect(attestation).toEqual({
      id: 'id1', rawId: 'AQ', type: 'public-key', clientExtensionResults: { credProps: {} },
      response: { clientDataJSON: 'Ag', attestationObject: 'Aw', transports: ['usb'] },
    });
    const bare = JSON.parse(credentialJSON({ id: 'i', rawId: buf(1), type: 'public-key', response: { clientDataJSON: buf(2), attestationObject: buf(3) } }));
    expect(bare.response.transports).toEqual([]);
    expect(bare.clientExtensionResults).toEqual({});
    const assertion = JSON.parse(
      credentialJSON({ id: 'i', rawId: buf(1), type: 'public-key',
        response: { clientDataJSON: buf(2), authenticatorData: buf(4), signature: buf(5), userHandle: buf(6) } }),
    );
    expect(assertion.response).toEqual({ clientDataJSON: 'Ag', authenticatorData: 'BA', signature: 'BQ', userHandle: 'Bg' });
    const noHandle = JSON.parse(
      credentialJSON({ id: 'i', rawId: buf(1), type: 'public-key', response: { clientDataJSON: buf(2), authenticatorData: buf(4), signature: buf(5), userHandle: null } }),
    );
    expect(noHandle.response.userHandle).toBeNull();
  });
});

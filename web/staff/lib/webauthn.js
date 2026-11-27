// Converts between the server's WebAuthn JSON (base64url strings) and the
// ArrayBuffers navigator.credentials expects.

export function fromB64url(s) {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/') + '==='.slice((s.length + 3) % 4);
  return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)).buffer;
}

export function toB64url(buf) {
  let s = '';
  for (const b of new Uint8Array(buf)) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

const withIds = (list = []) => list.map((c) => ({ ...c, id: fromB64url(c.id) }));

export function creationOptions(options) {
  const pk = options.publicKey;
  return {
    publicKey: {
      ...pk,
      challenge: fromB64url(pk.challenge),
      user: { ...pk.user, id: fromB64url(pk.user.id) },
      excludeCredentials: withIds(pk.excludeCredentials),
    },
  };
}

export function requestOptions(options) {
  const pk = options.publicKey;
  return { publicKey: { ...pk, challenge: fromB64url(pk.challenge), allowCredentials: withIds(pk.allowCredentials) } };
}

// Serializes a PublicKeyCredential (attestation or assertion) for the server.
export function credentialJSON(cred) {
  const r = cred.response;
  const response = { clientDataJSON: toB64url(r.clientDataJSON) };
  if (r.attestationObject) {
    response.attestationObject = toB64url(r.attestationObject);
    response.transports = r.getTransports?.() ?? [];
  } else {
    response.authenticatorData = toB64url(r.authenticatorData);
    response.signature = toB64url(r.signature);
    response.userHandle = r.userHandle ? toB64url(r.userHandle) : null;
  }
  return JSON.stringify({ id: cred.id, rawId: toB64url(cred.rawId), type: cred.type, response,
    clientExtensionResults: cred.getClientExtensionResults?.() ?? {} });
}

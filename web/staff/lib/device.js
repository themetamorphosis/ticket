// A stable random ID for this gate device, so the server can recognize a
// retry of the same scan request. Kept in localStorage when available.
const KEY = 'kc_device_id';
let memory = null;

export function deviceId() {
  try {
    const existing = window.localStorage.getItem(KEY);
    if (existing) return existing;
    const id = `dev-${crypto.randomUUID()}`;
    window.localStorage.setItem(KEY, id);
    return id;
  } catch {
    memory ??= `dev-${crypto.randomUUID()}`;
    return memory;
  }
}

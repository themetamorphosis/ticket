// Money is integer minor units; every currency in scope has two decimals.
export function formatMoney(minor, currency) {
  return new Intl.NumberFormat('en', { style: 'currency', currency }).format(minor / 100);
}

// Event times always render in the event's own time zone, never the device's.
export function formatDateTime(iso, timeZone) {
  return new Intl.DateTimeFormat('en-GB', {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
    timeZone,
    timeZoneName: 'short',
  }).format(new Date(iso));
}

export function formatTime(iso, timeZone) {
  return new Intl.DateTimeFormat('en-GB', {
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
    timeZone,
    timeZoneName: 'short',
  }).format(new Date(iso));
}

// mm:ss for a non-negative number of milliseconds.
export function formatCountdown(ms) {
  const total = Math.max(0, Math.ceil(ms / 1000));
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${m}:${String(s).padStart(2, '0')}`;
}

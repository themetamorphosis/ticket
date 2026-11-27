export function money(minor, currency = 'BDT') {
  return new Intl.NumberFormat('en', { style: 'currency', currency }).format(minor / 100);
}

export function when(iso) {
  return iso
    ? new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium', timeStyle: 'short', timeZone: 'Asia/Dhaka' }).format(new Date(iso))
    : '—';
}

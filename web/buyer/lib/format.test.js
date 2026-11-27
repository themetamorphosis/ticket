import { describe, expect, it } from 'vitest';
import { formatCountdown, formatDateTime, formatMoney, formatTime } from './format';

describe('format', () => {
  it('formats minor units as currency', () => {
    expect(formatMoney(155000, 'BDT')).toMatch(/1,550\.00/);
    expect(formatMoney(5, 'USD')).toBe('$0.05');
  });

  it('formats in the event time zone, not the device zone', () => {
    const iso = '2027-02-14T13:30:00Z';
    expect(formatDateTime(iso, 'Asia/Dhaka')).toMatch(/Sunday,? 14 February 2027.*19:30/);
    expect(formatTime(iso, 'Asia/Dhaka')).toMatch(/^19:30/);
    expect(formatTime(iso, 'UTC')).toMatch(/^13:30/);
  });

  it('formats countdowns as m:ss and never below zero', () => {
    expect(formatCountdown(600000)).toBe('10:00');
    expect(formatCountdown(61001)).toBe('1:02');
    expect(formatCountdown(-5)).toBe('0:00');
  });
});

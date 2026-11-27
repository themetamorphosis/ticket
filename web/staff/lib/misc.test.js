import { afterEach, describe, expect, it, vi } from 'vitest';
import { money, when } from './format';
import { beep } from './sound';

describe('format', () => {
  it('formats money and Dhaka times', () => {
    expect(money(155000)).toMatch(/1,550\.00/);
    expect(money(5, 'USD')).toBe('$0.05');
    expect(when('2027-02-14T13:30:00Z')).toMatch(/14 Feb 2027.*19:30/);
    expect(when(null)).toBe('—');
  });
});

describe('beep', () => {
  it('plays two high tones for admit and one low tone otherwise', () => {
    const freqs = [];
    class Ctx {
      currentTime = 0;
      destination = {};
      createOscillator() {
        const o = { frequency: {}, connect: vi.fn(), start: vi.fn(), stop: vi.fn() };
        freqs.push(o);
        return o;
      }
    }
    beep(true, Ctx);
    beep(false, Ctx);
    expect(freqs.map((o) => o.frequency.value)).toEqual([880, 1320, 220]);
  });

  it('is silent when audio is unavailable', () => {
    expect(() => beep(true, undefined)).not.toThrow();
  });
});

describe('deviceId', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.resetModules();
    window.localStorage.clear();
  });

  it('creates one ID and keeps it', async () => {
    const { deviceId } = await import('./device');
    const id = deviceId();
    expect(id).toMatch(/^dev-[0-9a-f-]{36}$/);
    expect(deviceId()).toBe(id);
  });

  it('falls back to memory when storage is blocked', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked');
    });
    const { deviceId } = await import('./device');
    const id = deviceId();
    expect(deviceId()).toBe(id);
  });
});

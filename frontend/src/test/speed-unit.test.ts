import { describe, expect, it } from 'vitest';

import { fromBps, kbsToUnit, normalizeSpeedUnit, toBps } from '@/lib/clients/speedUnit';

describe('speed units', () => {
  it('uses tc bit rates and keeps a byte value exact', () => {
    expect(toBps(1, 'kbit')).toBe(125);
    expect(toBps(1, 'mbit')).toBe(125_000);
    expect(toBps(1000, 'kbit')).toBe(125_000);
    expect(fromBps(125_000, 'mbit')).toBe(1);
    expect(toBps(fromBps(1_250_000, 'mbit'), 'mbit')).toBe(1_250_000);
    expect(toBps(fromBps(125_000, 'kbit'), 'kbit')).toBe(125_000);
  });

  it('maps retired byte units to Mbit/s', () => {
    expect(normalizeSpeedUnit(undefined)).toBe('mbit');
    expect(normalizeSpeedUnit('')).toBe('mbit');
    expect(normalizeSpeedUnit('kb')).toBe('mbit');
    expect(normalizeSpeedUnit('mb')).toBe('mbit');
    expect(normalizeSpeedUnit('kbit')).toBe('kbit');
  });

  it('converts KiB/s stats into tc Mbit/s and kbit/s', () => {
    const oneMbitAsKiB = 125_000 / 1024;
    expect(kbsToUnit(oneMbitAsKiB, 'mbit')).toBeCloseTo(1, 8);
    expect(kbsToUnit(oneMbitAsKiB, 'kbit')).toBeCloseTo(1000, 6);
  });
});

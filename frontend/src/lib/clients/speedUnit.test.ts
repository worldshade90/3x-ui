import { describe, expect, it } from 'vitest';

import { fromBps, kbsToUnit, toBps } from './speedUnit';

describe('speed units', () => {
  it('converts MB/s and KB/s through bytes without losing an integer rate', () => {
    expect(toBps(1, 'mb')).toBe(1024 * 1024);
    expect(toBps(1024, 'kb')).toBe(1024 * 1024);
    expect(fromBps(1024 * 1024, 'mb')).toBe(1);
    expect(toBps(fromBps(1_250_000, 'kb'), 'kb')).toBe(1_250_000);
  });

  it('shows stats MB/s as KB/s divided by 1024', () => {
    expect(kbsToUnit(2048, 'kb')).toBe(2048);
    expect(kbsToUnit(2048, 'mb')).toBe(2);
  });
});

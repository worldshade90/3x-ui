export type SpeedUnit = 'kb' | 'mb';

// Caps are stored as bytes/s. 1 KB/s = 1024 B/s and 1 MB/s = 1024 KB/s.
// That is about 8.389 Mbit/s per MB/s, because tc's mbit is 10^6 bit/s.
export const BYTES_PER_KB = 1024;
export const BYTES_PER_MB = 1024 * 1024;

export function normalizeSpeedUnit(unit: string | undefined): SpeedUnit {
  return unit === 'mb' ? 'mb' : 'kb';
}

export function toBps(value: number, unit: SpeedUnit): number {
  if (!Number.isFinite(value) || value <= 0) return 0;
  const scale = unit === 'mb' ? BYTES_PER_MB : BYTES_PER_KB;
  return Math.round(value * scale);
}

export function fromBps(bps: number, unit: SpeedUnit): number {
  if (!Number.isFinite(bps) || bps <= 0) return 0;
  const scale = unit === 'mb' ? BYTES_PER_MB : BYTES_PER_KB;
  return bps / scale;
}

// Stats samples are already KB/s (1024-based). MB/s is that value / 1024.
export function kbsToUnit(kbs: number, unit: SpeedUnit): number {
  if (!Number.isFinite(kbs) || kbs <= 0) return 0;
  return unit === 'mb' ? kbs / BYTES_PER_KB : kbs;
}

export function formatSpeed(kbs: number, unit: SpeedUnit): string {
  const value = kbsToUnit(kbs, unit);
  if (value <= 0) return '0';
  if (value >= 100) return String(Math.round(value));
  return String(Math.round(value * 1000) / 1000);
}

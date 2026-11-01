export type SpeedUnit = 'kbit' | 'mbit';

// Caps are stored as bytes/s. tc's kbit is 1000 bit/s = 125 B/s,
// and its mbit is 10^6 bit/s = 125000 B/s (1000 kbit/s).
export const BYTES_PER_KBIT = 125;
export const BYTES_PER_MBIT = 125_000;

export function normalizeSpeedUnit(unit: string | undefined): SpeedUnit {
  return unit === 'kbit' ? 'kbit' : 'mbit';
}

export function toBps(value: number, unit: SpeedUnit): number {
  if (!Number.isFinite(value) || value <= 0) return 0;
  const scale = unit === 'kbit' ? BYTES_PER_KBIT : BYTES_PER_MBIT;
  return Math.round(value * scale);
}

export function fromBps(bps: number, unit: SpeedUnit): number {
  if (!Number.isFinite(bps) || bps <= 0) return 0;
  const scale = unit === 'kbit' ? BYTES_PER_KBIT : BYTES_PER_MBIT;
  return bps / scale;
}

// Stats samples are KiB/s (1024 B). Bit units follow tc, not 1024.
export function kbsToUnit(kbs: number, unit: SpeedUnit): number {
  if (!Number.isFinite(kbs) || kbs <= 0) return 0;
  const bytes = kbs * 1024;
  return unit === 'kbit' ? bytes / BYTES_PER_KBIT : bytes / BYTES_PER_MBIT;
}

export function formatDisplay(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0';
  if (value >= 100) return String(Math.round(value));
  return String(Math.round(value * 1000) / 1000);
}

export function formatSpeed(kbs: number, unit: SpeedUnit): string {
  return formatDisplay(kbsToUnit(kbs, unit));
}

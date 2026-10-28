const DAY = 24 * 60 * 60 * 1000;
const SOON_DAYS = 30;

/** How close a certificate is to expiry: `soon` under 30 days, `expired` once past. */
export function expiryState(notAfter: string, now: Date): 'ok' | 'soon' | 'expired' {
  const left = new Date(notAfter).getTime() - now.getTime();
  if (left <= 0) {
    return 'expired';
  }
  return left < SOON_DAYS * DAY ? 'soon' : 'ok';
}

import type { PrintJobStatus } from './events';

// The sentence a screen reader announces for a status change. One place so desktop and mobile say the same thing.
export function describeStatus(s: PrintJobStatus): string {
  switch (s.state) {
    case 'queued': return 'Print queued';
    case 'sending': return 'Sending to the printer';
    case 'printing': return `Printing page ${s.page} of ${s.pageCount}`;
    case 'done': return 'Print finished';
    case 'cancelled': return 'Print cancelled';
    case 'failed': return `Print stopped: ${s.reason ?? 'unknown reason'}`;
  }
}

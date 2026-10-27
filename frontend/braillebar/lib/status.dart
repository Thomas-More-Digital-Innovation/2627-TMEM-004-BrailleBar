import 'api.dart';

// The sentence a screen reader announces for a status change. One place so every screen says the same thing.
String describeStatus(JobStatus s) => switch (s.state) {
      'queued' => 'Print queued',
      'sending' => 'Sending to the printer',
      'printing' => 'Printing page ${s.page} of ${s.pageCount}',
      'done' => 'Print finished',
      'cancelled' => 'Print cancelled',
      'failed' => 'Print stopped: ${s.reason.isEmpty ? 'unknown reason' : s.reason}',
      _ => 'Unknown print status',
    };

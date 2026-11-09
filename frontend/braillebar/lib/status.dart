import 'api.dart';

// The sentence a screen reader says for a status change, or null when it is not worth interrupting for.
String? describeStatus(JobStatus s) => switch (s.state) {
      'printing' => s.page > 1 ? 'Page ${s.page} of ${s.pageCount}' : 'Printing',
      'done' => "Finished, you can take your paper",
      'cancelled' => 'Stopped',
      'failed' => s.reason.isEmpty ? 'Something went wrong, your text was not printed' : s.reason,
      _ => null,
    };

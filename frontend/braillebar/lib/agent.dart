import 'dart:async';

import 'package:braillebar/api.dart';
import 'package:braillebar/protocol.dart';
import 'package:flutter/foundation.dart';

// The connection to a printer: BLE, or FakePrinter without hardware.
abstract class PrinterLink {
  /// Throws PrinterUnavailable. Does nothing when already connected.
  Future<void> connect();
  ValueListenable<bool> get connected;
  Future<void> send(String line);
  Stream<String> get replies;
}

class PrinterUnavailable implements Exception {
  PrinterUnavailable(this.cause);
  final String cause;
}

// Takes queued jobs and sends them to the printer, one job at a time.
class Agent {
  Agent(this.api, this.link, {this.timeout = const Duration(seconds: 3), this.retry = const Duration(seconds: 5)}) {
    link.replies.listen(_onReply);
    link.connected.addListener(() => link.connected.value ? resumeOpen() : findPrinter());
  }

  final Api api;
  final PrinterLink link;
  final Duration timeout;
  final Duration retry; // pause between searches for the printer

  Future<void>? _search;

  /// Completes once a printer is connected, however long that takes.
  // ponytail: scans forever while the printer is off, which costs phone battery; add a backoff if that shows.
  Future<void> findPrinter() => _search ??= _searchLoop().whenComplete(() => _search = null);

  Future<void> _searchLoop() async {
    while (!link.connected.value) {
      try {
        await link.connect();
      } on PrinterUnavailable {
        await Future.delayed(retry);
      }
    }
  }

  Future<void> _queue = Future.value();
  Completer<bool>? _waiting;
  int _waitingSeq = -1;
  String _printerState = '';

  /// The returned future completes when every job handed over so far is finished.
  Future<void> handle(JobStatus s) {
    if (s.state == 'queued') _queue = _queue.then((_) => _print(s.jobId));
    return _queue;
  }

  /// Picks up jobs that were missed while offline. Waits for a printer, so they don't fail just because it is off.
  /// A job that lands in the queue twice is harmless: _print re-reads it and skips it once finished.
  Future<void> resumeOpen() async {
    if (!link.connected.value) return;
    try {
      for (final s in await api.openJobs()) {
        _queue = _queue.then((_) => _print(s.jobId));
      }
    } catch (_) {
      // server unreachable: the next reconnect tries again
    }
    return _queue;
  }

  void _onReply(String line) {
    final m = parsePrinterMessage(line);
    if (m is PrinterState) _printerState = m.state;
    final waiting = _waiting;
    if (waiting == null || waiting.isCompleted) return;
    if (m is Ack && m.seq == _waitingSeq) waiting.complete(true);
    if (m is Nak && m.seq == _waitingSeq) waiting.complete(false);
  }

  static const _notPrinted = 'Something went wrong, your text was not printed';

  Future<void> _print(String jobId) async {
    var page = 0;
    try {
      final job = await api.getJob(jobId);
      if (const {'done', 'failed', 'cancelled'}.contains(job.state)) return;
      await findPrinter(); // a missing printer is not a failure: the text waits for it
      final pages = await api.getPages(jobId);
      var seq = 0; // continues across pages, so a seq is unique within the job
      for (final lines in pages) {
        page++;
        // Resuming: earlier pages are done. The firmware ACKs lines of this page it already printed without
        // printing them again. ponytail: not after a printer reset, then the interrupted page prints twice.
        if (page < job.page) {
          seq += lines.length;
          continue;
        }
        var printing = page == job.page && job.state == 'printing';
        if (!printing) await api.reportStatus(jobId, 'sending', page);
        Future<void> markPrinting() async {
          if (printing) return;
          printing = true;
          await api.reportStatus(jobId, 'printing', page);
        }

        for (final line in lines) {
          if (!await _sendLine(encodeChunk(jobId, seq, toPrinterText(line)), seq, markPrinting)) {
            await _fail(jobId, page, 'The printer has a problem. Turn it off and on again');
            return;
          }
          seq++;
          await markPrinting(); // a short line can be ACKed before the printer ever said busy
        }
        await markPrinting();
      }
      await api.reportStatus(jobId, 'done', page);
    } on ApiError catch (e) {
      if (e.status != 409) await _fail(jobId, page, _notPrinted); // 409: already handled
    } catch (_) {
      await _fail(jobId, page, _notPrinted);
    }
  }

  // The ACK only comes after the line is embossed, which takes minutes. The firmware ignores a resend of the line
  // it is printing, so resending is safe, and STATUS tells a busy printer from a gone one.
  Future<bool> _sendLine(String line, int seq, Future<void> Function() onBusy) async {
    for (var tries = 0; tries < 3; tries++) {
      final reply = Completer<bool>();
      _waiting = reply;
      _waitingSeq = seq;
      _printerState = '';
      try {
        await link.send(line);
        await link.send(statusCommand);
      } on PrinterUnavailable {
        // the printer went away mid-line: wait for it, then resend (it never prints a line twice)
        await Future.delayed(retry);
        await findPrinter();
        tries = -1;
        continue;
      }
      if (await reply.future.timeout(timeout, onTimeout: () => false)) return true;
      if (_printerState == 'error') return false;
      if (_printerState == 'busy') await onBusy();
      if (_printerState == 'busy' || _printerState == 'paused') tries = -1; // still working, keep waiting
    }
    return false;
  }

  Future<void> _fail(String jobId, int page, String reason) async {
    try {
      await api.reportStatus(jobId, 'failed', page, reason);
    } catch (_) {
      // ponytail: if the server is unreachable too, the job stays open; retry belongs with the reconnect work.
    }
  }
}

// Stands in for the ESP32: flutter run --dart-define=FAKE_PRINTER=true
class FakePrinter implements PrinterLink {
  FakePrinter({this.nakFirst = false, this.silent = false});
  final bool nakFirst, silent;

  final sent = <String>[];
  final _replies = StreamController<String>.broadcast();

  @override
  final ValueNotifier<bool> connected = ValueNotifier(false);

  @override
  Future<void> connect() async => connected.value = true;

  @override
  Stream<String> get replies => _replies.stream;

  @override
  Future<void> send(String line) async {
    debugPrint('fake printer: ${line.trim()}'); // shows in the terminal running flutter run
    if (!line.startsWith('CHUNK|')) return;
    final first = sent.isEmpty;
    sent.add(line);
    if (silent) return;
    final seq = line.split('|')[2];
    _replies.add(nakFirst && first ? 'NAK|$seq' : 'ACK|$seq');
  }
}

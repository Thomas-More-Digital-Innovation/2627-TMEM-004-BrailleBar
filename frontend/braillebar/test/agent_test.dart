import 'dart:async';
import 'dart:convert';

import 'package:braillebar/agent.dart';
import 'package:braillebar/api.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

final queued = JobStatus.fromJson({'jobId': 'j1', 'documentId': 'd1', 'state': 'queued', 'page': 0, 'pageCount': 2});

// Two pages of lines; every status report is recorded as state:page.
Api fakeApi(List<String> reports, {int reportCode = 204, String state = 'queued', int page = 0, bool open = false}) =>
    Api('http://x', 'dev1', client: MockClient((req) async {
      final job = {'jobId': 'j1', 'documentId': 'd1', 'state': state, 'page': page, 'pageCount': 2};
      if (req.method == 'GET') {
        if (req.url.path == '/jobs') return http.Response(jsonEncode({'jobs': open ? [job] : []}), 200);
        if (req.url.path == '/jobs/j1') return http.Response(jsonEncode(job), 200);
        return http.Response(jsonEncode({'jobId': 'j1', 'pages': [['ab', 'cd'], ['ef']]}), 200);
      }
      final b = jsonDecode(req.body);
      reports.add('${b['state']}:${b['page']}');
      return http.Response('', reportCode);
    }));

Agent agent(Api api, PrinterLink printer) =>
    Agent(api, printer, timeout: const Duration(milliseconds: 5), retry: const Duration(milliseconds: 1));

// Not found the first times it is searched for, like a printer that gets switched on later.
class LatePrinter extends FakePrinter {
  var misses = 3;

  @override
  Future<void> connect() async {
    if (misses-- > 0) throw PrinterUnavailable('off');
    await super.connect();
  }
}

// The link drops once while sending.
class DroppingPrinter extends FakePrinter {
  var dropped = false;

  @override
  Future<void> send(String line) async {
    if (!dropped) {
      dropped = true;
      throw PrinterUnavailable('gone');
    }
    await super.send(line);
  }
}

// Like the firmware: chunks get no direct answer, STATUS answers with the next state; out of states means printed.
class ScriptedPrinter implements PrinterLink {
  ScriptedPrinter(this.states);
  final List<String> states;
  final _replies = StreamController<String>.broadcast();
  var chunks = 0;
  var _seq = '';

  @override
  final connected = ValueNotifier(true);

  @override
  Future<void> connect() async {}

  @override
  Stream<String> get replies => _replies.stream;

  @override
  Future<void> send(String line) async {
    if (line.startsWith('CHUNK|')) {
      chunks++;
      _seq = line.split('|')[2];
    } else {
      _replies.add(states.isEmpty ? 'ACK|$_seq' : 'STATE|${states.removeAt(0)}');
    }
  }
}

void main() {
  test('sends every line and reports each step', () async {
    final reports = <String>[];
    final printer = FakePrinter();
    await agent(fakeApi(reports), printer).handle(queued);

    expect(printer.sent, ['CHUNK|j1|0|ab|03\n', 'CHUNK|j1|1|cd|07\n', 'CHUNK|j1|2|ef|03\n']);
    expect(reports, ['sending:1', 'printing:1', 'sending:2', 'printing:2', 'done:2']);
  });

  test('a missing printer does not fail the job, it waits', () async {
    final reports = <String>[];
    final printer = LatePrinter();
    await agent(fakeApi(reports), printer).handle(queued);
    expect(printer.misses, lessThan(0));
    expect(reports.first, 'sending:1');
    expect(reports.last, 'done:2');
  });

  test('a printer that drops mid-job gets the line again', () async {
    final reports = <String>[];
    final printer = DroppingPrinter();
    await agent(fakeApi(reports), printer).handle(queued);
    expect(printer.sent.length, 3);
    expect(reports.last, 'done:2');
  });

  test('a NAK resends the same line', () async {
    final reports = <String>[];
    final printer = FakePrinter(nakFirst: true);
    await agent(fakeApi(reports), printer).handle(queued);

    expect(printer.sent.length, 4);
    expect(printer.sent[0], printer.sent[1]);
    expect(reports.last, 'done:2');
  });

  test('a silent printer fails the job after 3 tries', () async {
    final reports = <String>[];
    final printer = FakePrinter(silent: true);
    await agent(fakeApi(reports), printer).handle(queued);

    expect(printer.sent.length, 3);
    expect(reports, ['sending:1', 'failed:1']);
  });

  test('a busy printer is waited for, however long the line takes', () async {
    final reports = <String>[];
    await agent(fakeApi(reports), ScriptedPrinter(['busy', 'busy', 'busy', 'busy', 'busy'])).handle(queued);
    expect(reports, ['sending:1', 'printing:1', 'sending:2', 'printing:2', 'done:2']); // printing only once per page
  });

  test('a printer in error fails the job at once', () async {
    final reports = <String>[];
    final printer = ScriptedPrinter(['error']);
    await agent(fakeApi(reports), printer).handle(queued);
    expect(printer.chunks, 1);
    expect(reports, ['sending:1', 'failed:1']);
  });

  test('a job the server already moved on is left alone', () async {
    final reports = <String>[];
    final printer = FakePrinter();
    await agent(fakeApi(reports, reportCode: 409), printer).handle(queued);

    expect(printer.sent, isEmpty);
    expect(reports, ['sending:1']);
  });

  test('open jobs are picked up once a printer is connected', () async {
    final reports = <String>[];
    final printer = FakePrinter();
    final a = agent(fakeApi(reports, open: true), printer);
    await a.resumeOpen();
    expect(reports, isEmpty, reason: 'no printer yet');

    printer.connected.value = true; // starts the resume by itself
    await a.resumeOpen();
    expect(reports.first, 'sending:1');
    expect(reports.last, 'done:2');
  });

  test('an interrupted job resumes on its page with the right seq', () async {
    final reports = <String>[];
    final printer = FakePrinter();
    await agent(fakeApi(reports, state: 'printing', page: 2), printer).handle(queued);
    expect(printer.sent, ['CHUNK|j1|2|ef|03\n']);
    expect(reports, ['done:2']);
  });

  test('a finished job is skipped', () async {
    final reports = <String>[];
    final printer = FakePrinter();
    await agent(fakeApi(reports, state: 'done', page: 2), printer).handle(queued);
    expect(printer.sent, isEmpty);
    expect(reports, isEmpty);
  });

  test('other states are ignored', () async {
    final reports = <String>[];
    final running = JobStatus.fromJson({'jobId': 'j1', 'documentId': 'd1', 'state': 'printing', 'page': 1, 'pageCount': 2});
    await agent(fakeApi(reports), FakePrinter()).handle(running);
    expect(reports, isEmpty);
  });
}

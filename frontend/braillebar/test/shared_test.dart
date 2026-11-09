import 'dart:convert';

import 'package:braillebar/api.dart';
import 'package:braillebar/protocol.dart';
import 'package:braillebar/status.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

void main() {
  group('protocol', () {
    test('checksum is the XOR of the bytes as 2 hex digits', () {
      expect(checksum(''), '00');
      expect(checksum('hi'), '01'); // 0x68 ^ 0x69
    });

    test('encodeChunk builds a line the firmware accepts', () {
      expect(encodeChunk('job-1', 0, 'hi'), 'CHUNK|job-1|0|hi|01\n');
    });

    test('encodeChunk rejects what the printer cannot take', () {
      expect(() => encodeChunk('bad|id', 0, 'hi'), throwsArgumentError);
      expect(() => encodeChunk('job', -1, 'hi'), throwsArgumentError);
      expect(() => encodeChunk('job', 0, 'a' * (maxCells + 1)), throwsArgumentError);
      expect(() => encodeChunk('job', 0, 'a|b'), throwsArgumentError);
      expect(() => encodeChunk('job', 0, 'café'), throwsArgumentError);
    });

    test('toPrinterText keeps letters and spaces only', () {
      expect(toPrinterText('Hi, you 2!'), 'Hi  you   ');
    });

    test('parsePrinterMessage', () {
      expect((parsePrinterMessage('ACK|3\n') as Ack).seq, 3);
      expect((parsePrinterMessage('NAK|0') as Nak).seq, 0);
      expect((parsePrinterMessage('STATE|paper_out') as PrinterState).state, 'paper_out');
      expect(parsePrinterMessage('ACK|x'), isNull);
      expect(parsePrinterMessage('STATE|on_fire'), isNull);
      expect(parsePrinterMessage('HELLO'), isNull);
    });
  });

  group('api', () {
    test('sends the device id and parses JSON', () async {
      late http.Request seen;
      final api = Api('http://x', 'dev1', client: MockClient((req) async {
        seen = req;
        return http.Response(jsonEncode({'documentId': 'd1'}), 201);
      }));
      expect(await api.submitText('hi'), 'd1');
      expect(seen.url.toString(), 'http://x/documents/text');
      expect(seen.headers['X-Device-Id'], 'dev1');
      expect(jsonDecode(seen.body), {'text': 'hi'});
    });

    test('reportStatus accepts the empty 204', () async {
      late http.Request seen;
      final api = Api('http://x', 'dev1', client: MockClient((req) async {
        seen = req;
        return http.Response('', 204);
      }));
      await api.reportStatus('j1', 'printing', 1);
      expect(seen.url.toString(), 'http://x/jobs/j1/status');
      expect(jsonDecode(seen.body), {'state': 'printing', 'page': 1, 'reason': ''});
    });

    test('getPages returns lines per page', () async {
      final api = Api('http://x', 'dev1', client: MockClient((req) async => http.Response(
            jsonEncode({'jobId': 'j1', 'pages': [['a', ''], ['b']]}), 200)));
      expect(await api.getPages('j1'), [['a', ''], ['b']]);
    });

    test('throws ApiError on a failed request', () async {
      final api = Api('http://x', 'dev1', client: MockClient((req) async => http.Response('nope', 409)));
      expect(api.getJob('j'), throwsA(isA<ApiError>().having((e) => e.status, 'status', 409)));
    });
  });

  test('describeStatus', () {
    JobStatus s(String state, [String reason = '']) => JobStatus.fromJson(
        {'jobId': 'j', 'documentId': 'd', 'state': state, 'page': 2, 'pageCount': 3, 'reason': reason});
    expect(describeStatus(s('printing')), 'Page 2 of 3');
    expect(describeStatus(s('failed', 'paper jam')), 'paper jam');
    expect(describeStatus(s('failed')), 'Something went wrong, your text was not printed');
    expect(describeStatus(s('sending')), isNull, reason: 'not worth interrupting for');
  });
}

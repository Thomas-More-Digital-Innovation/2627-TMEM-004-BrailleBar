import 'dart:convert';
import 'dart:io';

import 'package:braillebar/api.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('watchStatus reconnects after the server drops the socket', () async {
    // a real websocket server that sends one status per connection and then hangs up
    var connections = 0;
    final server = await HttpServer.bind('127.0.0.1', 0);
    server.listen((req) async {
      expect(req.uri.queryParameters['deviceId'], 'dev1');
      final ws = await WebSocketTransformer.upgrade(req);
      connections++;
      ws.add(jsonEncode({'jobId': 'j1', 'documentId': 'd1', 'state': 'printing', 'page': connections, 'pageCount': 2}));
      await ws.close();
    });
    addTearDown(() => server.close(force: true));

    final events = <String>[];
    final api = Api('http://127.0.0.1:${server.port}', 'dev1');
    final pages = await api
        .watchStatus(onConnection: (c) => events.add(c ? 'up' : 'down'), retry: const Duration(milliseconds: 10))
        .take(2)
        .map((s) => s.page)
        .toList();

    expect(pages, [1, 2]);
    expect(events.take(3), ['up', 'down', 'up']);
  });
}

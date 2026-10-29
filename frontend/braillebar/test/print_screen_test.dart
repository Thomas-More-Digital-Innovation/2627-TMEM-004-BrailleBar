import 'dart:async';
import 'dart:convert';

import 'package:braillebar/api.dart';
import 'package:braillebar/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

Map<String, dynamic> statusJson(String documentId, String state, int page) =>
    {'jobId': 'j1', 'documentId': documentId, 'state': state, 'page': page, 'pageCount': 1};

JobStatus status(String documentId, String state, int page) => JobStatus.fromJson(statusJson(documentId, state, page));

// A fake server: POST /documents/text gives document d1, GET /jobs/j1 answers with jobState.
Api fakeApi({String jobState = 'printing'}) => Api('http://x', 'dev1', client: MockClient((req) async {
      if (req.method == 'GET') return http.Response(jsonEncode(statusJson('d1', jobState, 1)), 200);
      return http.Response(jsonEncode({'documentId': 'd1'}), 201);
    }));

Future<void> typeAndPrint(WidgetTester tester) async {
  await tester.enterText(find.byType(TextField), 'hello');
  await tester.pump(); // the button enables on the next frame
  await tester.tap(find.text('Print'));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('prints text and shows only its own status', (tester) async {
    final statuses = StreamController<JobStatus>();
    await tester.pumpWidget(MaterialApp(home: PrintScreen(api: fakeApi(), watch: (_) => statuses.stream)));

    FilledButton button() => tester.widget<FilledButton>(find.byType(FilledButton));
    expect(button().onPressed, isNull, reason: 'nothing to print yet');

    // the queued status arrives before submitText has returned the document id
    statuses.add(status('d1', 'queued', 0));
    await typeAndPrint(tester);
    expect(find.text('Print queued'), findsOneWidget);

    statuses.add(status('other-doc', 'done', 1));
    statuses.add(status('d1', 'printing', 1));
    await tester.pumpAndSettle();
    expect(find.text('Printing page 1 of 1'), findsOneWidget);
  });

  testWidgets('a lost connection is shown once and the job is re-read after reconnecting', (tester) async {
    final statuses = StreamController<JobStatus>();
    late void Function(bool) connection;
    await tester.pumpWidget(MaterialApp(
      home: PrintScreen(api: fakeApi(jobState: 'done'), watch: (onConnection) {
        connection = onConnection;
        return statuses.stream;
      }),
    ));
    connection(true);
    statuses.add(status('d1', 'queued', 0));
    await typeAndPrint(tester);

    connection(false);
    await tester.pumpAndSettle();
    expect(find.text('Connection to the server lost, reconnecting'), findsOneWidget);

    // while offline the job finished; the screen learns it from GET /jobs/j1
    connection(true);
    await tester.pumpAndSettle();
    expect(find.text('Print finished'), findsOneWidget);
  });

  testWidgets('a server error is shown', (tester) async {
    final api = Api('http://x', 'dev1', client: MockClient((req) async => http.Response('bad', 400)));
    await tester.pumpWidget(MaterialApp(home: PrintScreen(api: api, watch: (_) => const Stream.empty())));
    await typeAndPrint(tester);
    expect(find.text('Could not send the text, error 400'), findsOneWidget);
  });
}

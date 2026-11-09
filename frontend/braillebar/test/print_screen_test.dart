import 'dart:async';
import 'dart:convert';

import 'package:braillebar/agent.dart';
import 'package:braillebar/api.dart';
import 'package:braillebar/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

Map<String, dynamic> statusJson(String documentId, String state, int page) =>
    {'jobId': 'j1', 'documentId': documentId, 'state': state, 'page': page, 'pageCount': 3};

JobStatus status(String documentId, String state, int page) => JobStatus.fromJson(statusJson(documentId, state, page));

// A fake server: POST /documents/text gives document d1, GET /jobs/j1 answers with jobState.
Api fakeApi({String jobState = 'printing'}) => Api('http://x', 'dev1', client: MockClient((req) async {
      if (req.method == 'GET') return http.Response(jsonEncode(statusJson('d1', jobState, 1)), 200);
      return http.Response(jsonEncode({'documentId': 'd1'}), 201);
    }));

FakePrinter connectedPrinter() => FakePrinter()..connected.value = true;

Future<void> typeAndPrint(WidgetTester tester) async {
  await tester.enterText(find.byType(TextField), 'hello');
  await tester.pump(); // the button enables on the next frame
  await tester.tap(find.text('Print'));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('prints text and says each step', (tester) async {
    final statuses = StreamController<JobStatus>();
    await tester.pumpWidget(MaterialApp(home: PrintScreen(api: fakeApi(), link: connectedPrinter(), watch: (_) => statuses.stream)));

    FilledButton button() => tester.widget<FilledButton>(find.byType(FilledButton));
    expect(button().onPressed, isNull, reason: 'nothing to print yet');

    // the queued status arrives before submitText has returned the document id
    statuses.add(status('d1', 'queued', 0));
    await typeAndPrint(tester);
    expect(find.text('Printing'), findsOneWidget);

    statuses.add(status('d1', 'sending', 2));
    await tester.pumpAndSettle();
    expect(find.text('Printing'), findsOneWidget, reason: 'sending is not announced');

    statuses.add(status('d1', 'printing', 2));
    await tester.pumpAndSettle();
    expect(find.text('Page 2 of 3'), findsOneWidget);

    statuses.add(status('d1', 'done', 3));
    await tester.pumpAndSettle();
    expect(find.text('Finished, you can take your paper'), findsOneWidget);
  });

  testWidgets('text sent earlier is announced as such', (tester) async {
    final statuses = StreamController<JobStatus>();
    await tester.pumpWidget(MaterialApp(home: PrintScreen(api: fakeApi(), link: connectedPrinter(), watch: (_) => statuses.stream)));
    statuses.add(status('old', 'printing', 1));
    await tester.pumpAndSettle();
    expect(find.text('Printing your earlier text'), findsOneWidget);
  });

  testWidgets('says the text waits when there is no printer', (tester) async {
    await tester.pumpWidget(MaterialApp(home: PrintScreen(api: fakeApi(), link: FakePrinter(), watch: (_) => const Stream.empty())));
    await typeAndPrint(tester);
    expect(find.text("The printer is off or too far away. Your text will print when it's found"), findsOneWidget);
  });

  testWidgets('a server reconnect is silent but the text is re-read', (tester) async {
    final statuses = StreamController<JobStatus>();
    late void Function(bool) connection;
    await tester.pumpWidget(MaterialApp(
      home: PrintScreen(api: fakeApi(jobState: 'done'), link: connectedPrinter(), watch: (onConnection) {
        connection = onConnection;
        return statuses.stream;
      }),
    ));
    connection(true);
    statuses.add(status('d1', 'queued', 0));
    await typeAndPrint(tester);

    connection(false);
    await tester.pumpAndSettle();
    expect(find.text('Printing'), findsOneWidget);

    // while offline the text finished; the screen learns it from GET /jobs/j1
    connection(true);
    await tester.pumpAndSettle();
    expect(find.text('Finished, you can take your paper'), findsOneWidget);
  });

  testWidgets('a server error says the text was not sent', (tester) async {
    final api = Api('http://x', 'dev1', client: MockClient((req) async => http.Response('bad', 400)));
    await tester.pumpWidget(MaterialApp(home: PrintScreen(api: api, link: connectedPrinter(), watch: (_) => const Stream.empty())));
    await typeAndPrint(tester);
    expect(find.text('Your text was not sent, please try again'), findsOneWidget);
  });
}

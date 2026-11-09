import 'package:braillebar/agent.dart';
import 'package:braillebar/printer_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('says whether a printer is connected and which one', (tester) async {
    final link = FakePrinter();
    await tester.pumpWidget(MaterialApp(home: PrinterScreen(link: link)));
    expect(find.text('No printer connected. Looking for the printer'), findsOneWidget);

    link.connected.value = true;
    await tester.pump();
    expect(find.text('Printer connected: BrailleMachine'), findsOneWidget);
    expect(find.byType(FilledButton), findsNothing);

    link.connected.value = false;
    await tester.pump();
    expect(find.text('No printer connected. Looking for the printer'), findsOneWidget);
  });
}

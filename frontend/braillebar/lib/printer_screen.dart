import 'package:braillebar/agent.dart';
import 'package:braillebar/ble_link.dart';
import 'package:braillebar/layout.dart';
import 'package:flutter/material.dart';

// Only shows the state: finding and connecting the printer happens by itself (Agent.findPrinter).
class PrinterScreen extends StatelessWidget {
  const PrinterScreen({super.key, required this.link});
  final PrinterLink link;

  @override
  Widget build(BuildContext context) {
    return AppLayout(
      title: 'Printer',
      child: Padding(
        padding: const EdgeInsets.fromLTRB(24, 16, 24, 24),
        child: ValueListenableBuilder(
          valueListenable: link.connected,
          builder: (context, connected, _) => Column(
            crossAxisAlignment: .stretch,
            children: [
              Text(
                connected ? 'Printer connected: $printerName' : 'No printer connected. Looking for the printer',
                style: Theme.of(context).textTheme.headlineMedium,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

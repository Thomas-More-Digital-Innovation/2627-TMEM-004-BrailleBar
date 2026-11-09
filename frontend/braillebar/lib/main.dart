import 'dart:async';

import 'package:braillebar/agent.dart';
import 'package:braillebar/api.dart';
import 'package:braillebar/ble_link.dart';
import 'package:braillebar/layout.dart';
import 'package:braillebar/printer_screen.dart';
import 'package:braillebar/status.dart';
import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final deviceId = await loadDeviceId();
  final myapi = Api(const String.fromEnvironment('API_URL', defaultValue: 'http://localhost:8080'), deviceId);
  final PrinterLink link = const bool.fromEnvironment('FAKE_PRINTER') ? FakePrinter() : BlePrinterLink();
  final agent = Agent(myapi, link);
  myapi.watchStatus(onConnection: (up) {
    if (up) agent.resumeOpen();
  }).listen(agent.handle);
  agent.findPrinter();
  runApp(MyApp(api: myapi, link: link));
}

class MyApp extends StatelessWidget {
  const MyApp({super.key, required this.api, required this.link});
  final Api api;
  final PrinterLink link;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'BrailleBar',
      theme: ThemeData(
        colorScheme: .fromSeed(seedColor: Colors.deepPurple),
      ),
      home: Home(api: api, link: link),
    );
  }
}

class Home extends StatefulWidget {
  const Home({super.key, required this.api, required this.link});
  final Api api;
  final PrinterLink link;

  @override
  State<Home> createState() => _HomeState();
}

class _HomeState extends State<Home> {
  int _tab = 0;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      // IndexedStack keeps both screens alive, so announcements are made on either tab
      body: IndexedStack(index: _tab, children: [
        PrintScreen(api: widget.api, link: widget.link),
        PrinterScreen(link: widget.link),
      ]),
      bottomNavigationBar: NavigationBar(
        selectedIndex: _tab,
        onDestinationSelected: (i) => setState(() => _tab = i),
        destinations: const [
          NavigationDestination(icon: Icon(Icons.edit), label: 'Type and print'),
          NavigationDestination(icon: Icon(Icons.print), label: 'Printer'),
        ],
      ),
    );
  }
}

class PrintScreen extends StatefulWidget {
  const PrintScreen({super.key, required this.api, required this.link, this.watch});
  final Api api;
  final PrinterLink link;
  // tests pass fake statuses and connection changes; the app uses api.watchStatus
  final Stream<JobStatus> Function(void Function(bool connected) onConnection)? watch;

  @override
  State<PrintScreen> createState() => _PrintScreenState();
}

class _PrintScreenState extends State<PrintScreen> {
  final _text = TextEditingController();
  late final StreamSubscription<JobStatus> _sub;
  // by documentId: a status can arrive before submitText has returned the id
  final _statuses = <String, JobStatus>{};
  String? _documentId;
  String _message = '';
  bool _sending = false;
  bool? _connected; // null until the first connection

  @override
  void initState() {
    super.initState();
    _text.addListener(() => setState(() {})); // enables the button as soon as there is text
    widget.link.connected.addListener(_onPrinter);
    final watch = widget.watch ?? (onConnection) => widget.api.watchStatus(onConnection: onConnection);
    _sub = watch(_onConnection).listen(_onStatus);
  }

  void _onPrinter() {
    _say(widget.link.connected.value ? 'Printer connected' : 'The printer is off or too far away');
  }

  void _onStatus(JobStatus s) {
    _statuses[s.documentId] = s;
    final own = s.documentId == _documentId;
    if (!own && _sending) return; // probably our own text, submitText just hasn't answered yet
    final sentence = !own && s.state == 'printing' && s.page == 1 ? 'Printing your earlier text' : describeStatus(s);
    if (sentence != null) _show(sentence);
  }

  // Quiet on purpose: statuses sent while offline are lost, so only re-read the text being printed.
  Future<void> _onConnection(bool connected) async {
    final wasLost = _connected == false;
    if (connected == _connected) return;
    _connected = connected;
    final last = _statuses[_documentId];
    if (!connected || !wasLost || last == null) return;
    try {
      _onStatus(await widget.api.getJob(last.jobId));
    } catch (_) {
      // the next status or reconnect brings it
    }
  }

  @override
  void dispose() {
    _sub.cancel();
    widget.link.connected.removeListener(_onPrinter);
    _text.dispose();
    super.dispose();
  }

  void _say(String message) {
    if (mounted) SemanticsService.sendAnnouncement(View.of(context), message, TextDirection.ltr);
  }

  // Shows the message and has the screen reader say it.
  void _show(String message) {
    if (!mounted) return;
    setState(() => _message = message);
    _say(message);
  }

  Future<void> _print() async {
    setState(() => _sending = true);
    try {
      final id = await widget.api.submitText(_text.text);
      _documentId = id;
      _text.clear();
      final s = _statuses[id];
      final sentence = s == null ? null : describeStatus(s);
      _show(widget.link.connected.value
          ? sentence ?? 'Printing'
          : "The printer is off or too far away. Your text will print when it's found");
    } on ApiError {
      _show('Your text was not sent, please try again');
    } catch (_) {
      _show('No internet connection, your text was not sent');
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AppLayout(
      title: 'Type and print',
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: .stretch,
          children: [
            TextField(
              controller: _text,
              decoration: const InputDecoration(labelText: 'Text to print', border: OutlineInputBorder()),
              minLines: 3,
              maxLines: 8,
            ),
            const SizedBox(height: 16),
            FilledButton(
              onPressed: _text.text.trim().isEmpty || _sending ? null : _print,
              child: Text(_sending ? 'Sending' : 'Print'),
            ),
            const SizedBox(height: 24),
            Text(_message, style: Theme.of(context).textTheme.headlineSmall),
          ],
        ),
      ),
    );
  }
}

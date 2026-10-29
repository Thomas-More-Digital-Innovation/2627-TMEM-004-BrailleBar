import 'dart:async';

import 'package:braillebar/api.dart';
import 'package:braillebar/layout.dart';
import 'package:braillebar/status.dart';
import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final deviceId = await loadDeviceId();
  final myapi = Api(const String.fromEnvironment('API_URL', defaultValue: 'http://localhost:8080'), deviceId);
  runApp(MyApp(api: myapi));
}

class MyApp extends StatelessWidget {
  const MyApp({super.key, required this.api});
  final Api api;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'BrailleBar',
      theme: ThemeData(
        colorScheme: .fromSeed(seedColor: Colors.deepPurple),
      ),
      home: PrintScreen(api: api),
    );
  }
}

class PrintScreen extends StatefulWidget {
  const PrintScreen({super.key, required this.api, this.watch});
  final Api api;
  // tests pass fake statuses and connection changes; the app uses api.watchStatus
  final Stream<JobStatus> Function(void Function(bool connected) onConnection)? watch;

  @override
  State<PrintScreen> createState() => _PrintScreenState();
}

class _PrintScreenState extends State<PrintScreen> {
  final _text = TextEditingController();
  late final StreamSubscription<JobStatus> _sub;
  // by documentId: the queued status can arrive before submitText has returned the id
  final _statuses = <String, JobStatus>{};
  String? _documentId;
  String _message = '';
  bool _sending = false;
  bool? _connected; // null until the first connection

  @override
  void initState() {
    super.initState();
    _text.addListener(() => setState(() {})); // enables the button as soon as there is text
    final watch = widget.watch ?? (onConnection) => widget.api.watchStatus(onConnection: onConnection);
    _sub = watch(_onConnection).listen(_onStatus);
  }

  void _onStatus(JobStatus s) {
    _statuses[s.documentId] = s;
    if (s.documentId == _documentId) _show(describeStatus(s));
  }

  // Every failed retry reports false again, so only a change is announced.
  Future<void> _onConnection(bool connected) async {
    final wasLost = _connected == false;
    if (connected == _connected) return;
    _connected = connected;
    if (!connected) return _show('Connection to the server lost, reconnecting');
    if (!wasLost) return; // the first connection is not news
    // Statuses sent while offline are lost, so ask for the job itself.
    final last = _statuses[_documentId];
    if (last == null) return _show('Reconnected');
    try {
      _onStatus(await widget.api.getJob(last.jobId));
    } catch (_) {
      _show('Reconnected');
    }
  }

  @override
  void dispose() {
    _sub.cancel();
    _text.dispose();
    super.dispose();
  }

  // Shows the message and has the screen reader say it.
  void _show(String message) {
    if (!mounted) return;
    setState(() => _message = message);
    SemanticsService.sendAnnouncement(View.of(context), message, TextDirection.ltr);
  }

  Future<void> _print() async {
    setState(() => _sending = true);
    try {
      final id = await widget.api.submitText(_text.text);
      _documentId = id;
      _text.clear();
      final s = _statuses[id];
      _show(s != null ? describeStatus(s) : 'Sent, waiting for the print queue');
    } on ApiError catch (e) {
      _show('Could not send the text, error ${e.status}');
    } catch (_) {
      _show('Could not reach the server');
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

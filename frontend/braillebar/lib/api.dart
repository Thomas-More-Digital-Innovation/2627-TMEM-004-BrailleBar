import 'dart:convert';
import 'dart:math';

import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

// Client for the API gateway (backend/gateway). Every request carries the anonymous device id; there are no accounts.

class ApiError implements Exception {
  ApiError(this.status, this.message);
  final int status;
  final String message;

  @override
  String toString() => 'ApiError $status: $message';
}

// queued -> sending -> printing -> done is the happy path. failed and cancelled end a job, reason says why.
class JobStatus {
  JobStatus.fromJson(Map<String, dynamic> j)
      : jobId = j['jobId'],
        documentId = j['documentId'],
        state = j['state'],
        page = j['page'],
        pageCount = j['pageCount'],
        reason = j['reason'] ?? '';

  final String jobId, documentId, state, reason;
  final int page, pageCount; // page is 1-based, 0 before printing starts
}

class Api {
  Api(this.baseUrl, this.deviceId, {http.Client? client}) : _http = client ?? http.Client();

  final String baseUrl; // no trailing slash
  final String deviceId;
  final http.Client _http;

  Future<dynamic> _call(String method, String path, [Object? body]) async {
    final req = http.Request(method, Uri.parse('$baseUrl$path'))..headers['X-Device-Id'] = deviceId;
    if (body != null) {
      req.headers['Content-Type'] = 'application/json';
      req.body = jsonEncode(body);
    }
    final res = await http.Response.fromStream(await _http.send(req));
    if (res.statusCode >= 300) throw ApiError(res.statusCode, res.body);
    return res.statusCode == 204 ? null : jsonDecode(res.body);
  }

  String _job(String id) => '/jobs/${Uri.encodeComponent(id)}';

  /// Returns the new document id.
  Future<String> submitText(String text) async =>
      (await _call('POST', '/documents/text', {'text': text}))['documentId'];

  Future<JobStatus> getJob(String jobId) async => JobStatus.fromJson(await _call('GET', _job(jobId)));

  /// This device's unfinished jobs, oldest first.
  Future<List<JobStatus>> openJobs() async =>
      [for (final j in (await _call('GET', '/jobs'))['jobs']) JobStatus.fromJson(j)];

  /// The laid-out lines per page, for the agent to send to the printer.
  Future<List<List<String>>> getPages(String jobId) async {
    final j = await _call('GET', '${_job(jobId)}/pages');
    return [
      for (final page in j['pages']) [for (final line in page) line as String],
    ];
  }

  /// A retry of the same report is fine; going back (or changing a finished job) throws ApiError 409.
  Future<void> reportStatus(String jobId, String state, int page, [String reason = '']) =>
      _call('POST', '${_job(jobId)}/status', {'state': state, 'page': page, 'reason': reason});

  /// Live statuses of this device's jobs. Never ends: a lost connection is retried after 1s, 2s, 4s ... up to 30s.
  /// onConnection reports every connect and disconnect. Statuses sent while disconnected are lost, so re-read
  /// open jobs with getJob after a reconnect.
  Stream<JobStatus> watchStatus({void Function(bool connected)? onConnection, Duration retry = const Duration(seconds: 1)}) async* {
    final uri = Uri.parse('${baseUrl.replaceFirst('http', 'ws')}/status')
        .replace(queryParameters: {'deviceId': deviceId});
    var delay = retry;
    while (true) {
      final channel = WebSocketChannel.connect(uri);
      try {
        await channel.ready;
        onConnection?.call(true);
        delay = retry;
        await for (final m in channel.stream) {
          yield JobStatus.fromJson(jsonDecode(m as String));
        }
      } catch (_) {
        // unreachable server or a broken message: both mean reconnect
      } finally {
        channel.sink.close(); // also runs when the listener cancels, so no socket is left open
      }
      onConnection?.call(false);
      await Future.delayed(delay);
      delay = delay * 2 > const Duration(seconds: 30) ? const Duration(seconds: 30) : delay * 2;
    }
  }
}

/// The anonymous device id: made once on first launch, then reused forever.
Future<String> loadDeviceId() async {
  final prefs = await SharedPreferences.getInstance();
  var id = prefs.getString('deviceId');
  if (id == null) {
    final r = Random.secure();
    id = List.generate(16, (_) => r.nextInt(256).toRadixString(16).padLeft(2, '0')).join();
    await prefs.setString('deviceId', id);
  }
  return id;
}

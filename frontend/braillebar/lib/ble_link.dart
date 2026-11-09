import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:braillebar/agent.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_blue_plus/flutter_blue_plus.dart';

// Values from firmware/BLESerial.h and firmware.ino.
const printerName = 'BrailleMachine';
final _service = Guid('FFE0');
final _rxUuid = Guid('FFE1'); // app -> printer
final _txUuid = Guid('FFE2'); // printer -> app; the firmware also notifies on FFE1, so subscribing to both doubles every reply

// A notification can hold part of a line or several lines.
class LineBuffer {
  var _rest = '';

  List<String> add(List<int> bytes) {
    final parts = (_rest + latin1.decode(bytes)).split('\n');
    _rest = parts.removeLast();
    return parts;
  }
}

class BlePrinterLink implements PrinterLink {
  final _replies = StreamController<String>.broadcast();
  BluetoothCharacteristic? _rx;
  Future<BluetoothCharacteristic>? _opening; // the screen and the agent can both ask at once, a second scan would throw
  StreamSubscription<BluetoothConnectionState>? _state;

  @override
  final ValueNotifier<bool> connected = ValueNotifier(false);

  @override
  Future<void> connect() => _connected();

  @override
  Stream<String> get replies => _replies.stream;

  @override
  Future<void> send(String line) async {
    final rx = await _connected();
    final bytes = ascii.encode(line);
    final size = max(rx.device.mtuNow - 3, 20); // a chunk is up to about 85 bytes, the default MTU fits 20
    try {
      for (var i = 0; i < bytes.length; i += size) {
        await rx.write(bytes.sublist(i, min(i + size, bytes.length)));
      }
    } on FlutterBluePlusException catch (e) {
      throw PrinterUnavailable('$e');
    }
  }

  // Connects on first use and again after the connection dropped.
  Future<BluetoothCharacteristic> _connected() async {
    final rx = _rx;
    if (rx != null && rx.device.isConnected) return rx;
    return _opening ??= _open().whenComplete(() => _opening = null);
  }

  Future<BluetoothCharacteristic> _open() async {
    BluetoothDevice? device;
    try {
      device = await _scan();
      await device.connect(license: License.nonprofit);
      final services = await device.discoverServices();
      final chars = services.firstWhere((s) => s.uuid == _service).characteristics;
      final tx = chars.firstWhere((c) => c.uuid == _txUuid);
      final buffer = LineBuffer();
      device.cancelWhenDisconnected(tx.onValueReceived.listen((v) => buffer.add(v).forEach(_replies.add)));
      await tx.setNotifyValue(true);
      final rx = chars.firstWhere((c) => c.uuid == _rxUuid);
      await _state?.cancel();
      _state = device.connectionState.listen((s) {
        if (s == BluetoothConnectionState.disconnected) connected.value = false;
      });
      connected.value = true;
      return _rx = rx;
    } catch (e) {
      // a half-open connection stops the printer advertising, so the next scan would never find it
      await device?.disconnect().catchError((_) {});
      throw PrinterUnavailable('$e');
    }
  }

  Future<BluetoothDevice> _scan() async {
    final found = FlutterBluePlus.onScanResults
        .expand((results) => results)
        .firstWhere((r) => r.advertisementData.advName == printerName || r.device.platformName == printerName)
        .timeout(const Duration(seconds: 10));
    found.ignore(); // if startScan throws, nobody awaits found and its timeout would be an uncaught error
    try {
      await FlutterBluePlus.startScan();
      return (await found).device;
    } finally {
      await FlutterBluePlus.stopScan();
    }
  }
}

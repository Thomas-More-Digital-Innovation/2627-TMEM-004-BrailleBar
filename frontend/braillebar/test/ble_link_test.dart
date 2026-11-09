import 'dart:convert';

import 'package:braillebar/ble_link.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('LineBuffer joins split notifications and splits joined ones', () {
    final b = LineBuffer();
    expect(b.add(ascii.encode('AC')), isEmpty);
    expect(b.add(ascii.encode('K|1\nSTATE|bu')), ['ACK|1']);
    expect(b.add(ascii.encode('sy\nACK|2\n')), ['STATE|busy', 'ACK|2']);
  });
}

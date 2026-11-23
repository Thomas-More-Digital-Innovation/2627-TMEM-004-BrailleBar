import 'dart:math';
import 'dart:typed_data';
import 'dart:ui';

import 'package:flutter/material.dart';

const paper = Color(0xFFE4E7E2);
const ink = Color(0xFF16181D);
const graphite = Color(0xFF565C66);
const cobalt = Color(0xFF1E3AD0);

// One dither cell in logical pixels: big enough that the dither reads as texture, like toner.
const _px = 2.0;
// 4x4 Bayer matrix, the thresholds of ordered dithering.
const _bayer = [0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5];
bool _on(double density, int gx, int gy) => density > (_bayer[(gy & 3) * 4 + (gx & 3)] + 0.5) / 16;

ThemeData paperTheme() {
  const shape = RoundedRectangleBorder(borderRadius: BorderRadius.all(Radius.circular(6)));
  const buttonText = TextStyle(fontFamily: 'Atkinson Hyperlegible', fontSize: 20, fontWeight: FontWeight.bold);
  return ThemeData(
    fontFamily: 'Atkinson Hyperlegible',
    colorScheme: const ColorScheme.light(
      primary: cobalt, onPrimary: paper, secondary: ink, onSecondary: paper,
      surface: paper, onSurface: ink, onSurfaceVariant: graphite, outline: ink, error: Color(0xFFB0201A),
    ),
    scaffoldBackgroundColor: Colors.transparent, // the grain behind every screen shows through
    appBarTheme: const AppBarTheme(
      backgroundColor: Colors.transparent, foregroundColor: ink, elevation: 0, scrolledUnderElevation: 0,
      centerTitle: false, titleSpacing: 24,
      toolbarTextStyle: TextStyle(fontFamily: 'Atkinson Hyperlegible', fontSize: 18, color: graphite),
    ),
    textTheme: const TextTheme(
      headlineMedium: TextStyle(fontSize: 34, height: 1.15, fontWeight: FontWeight.bold, color: ink),
      headlineSmall: TextStyle(fontSize: 28, height: 1.2, fontWeight: FontWeight.bold, color: ink),
      bodyLarge: TextStyle(fontSize: 20, height: 1.4, color: ink),
    ),
    filledButtonTheme: FilledButtonThemeData(style: FilledButton.styleFrom(
      minimumSize: const Size.fromHeight(64), shape: shape, textStyle: buttonText,
      disabledBackgroundColor: ink.withValues(alpha: 0.12), disabledForegroundColor: graphite,
    )),
    outlinedButtonTheme: OutlinedButtonThemeData(style: OutlinedButton.styleFrom(
      minimumSize: const Size.fromHeight(64), shape: shape, textStyle: buttonText,
      foregroundColor: ink, side: const BorderSide(color: ink, width: 1.5),
    )),
    inputDecorationTheme: const InputDecorationTheme(
      filled: true, fillColor: Color(0xFFEEF0EC),
      labelStyle: TextStyle(color: graphite, fontSize: 18),
      contentPadding: EdgeInsets.all(18),
      border: OutlineInputBorder(borderRadius: BorderRadius.all(Radius.circular(6))),
      enabledBorder: OutlineInputBorder(borderRadius: BorderRadius.all(Radius.circular(6)), borderSide: BorderSide(color: ink, width: 1.5)),
      focusedBorder: OutlineInputBorder(borderRadius: BorderRadius.all(Radius.circular(6)), borderSide: BorderSide(color: cobalt, width: 3)),
    ),
    navigationBarTheme: NavigationBarThemeData(
      backgroundColor: Colors.transparent, indicatorColor: ink, surfaceTintColor: Colors.transparent, height: 76,
      iconTheme: WidgetStateProperty.resolveWith((s) => IconThemeData(color: s.contains(WidgetState.selected) ? paper : ink)),
      labelTextStyle: const WidgetStatePropertyAll(TextStyle(fontFamily: 'Atkinson Hyperlegible', fontSize: 15, color: ink)),
    ),
  );
}

/// Paper with a fixed grain behind the whole app. Purely visual: hidden from screen readers.
class Paper extends StatelessWidget {
  const Paper({super.key, required this.child});
  final Widget child;

  @override
  Widget build(BuildContext context) => Stack(children: [
        Positioned.fill(child: ExcludeSemantics(child: RepaintBoundary(child: CustomPaint(painter: _Grain())))),
        child,
      ]);
}

class _Grain extends CustomPainter {
  @override
  void paint(Canvas canvas, Size size) {
    canvas.drawColor(paper, BlendMode.src);
    final rnd = Random(7); // fixed seed: the grain must not shimmer between paints
    final dark = <double>[], light = <double>[], toner = <double>[];
    final fadeFrom = size.height * 0.55;
    for (var gy = 0; gy * _px < size.height; gy++) {
      final y = gy * _px;
      // an ordered-dither fade toward the bottom, like toner thinning out on a page
      final fade = y < fadeFrom ? 0.0 : pow((y - fadeFrom) / (size.height - fadeFrom), 2) * 0.3;
      for (var gx = 0; gx * _px < size.width; gx++) {
        final x = gx * _px, r = rnd.nextDouble();
        if (_on(fade, gx, gy)) {
          toner..add(x)..add(y);
        } else if (r < 0.05) {
          dark..add(x)..add(y);
        } else if (r > 0.94) {
          light..add(x)..add(y);
        }
      }
    }
    final p = Paint()..strokeWidth = _px..strokeCap = StrokeCap.square;
    canvas.drawRawPoints(PointMode.points, Float32List.fromList(toner), p..color = ink.withValues(alpha: 0.16));
    canvas.drawRawPoints(PointMode.points, Float32List.fromList(dark), p..color = ink.withValues(alpha: 0.07));
    canvas.drawRawPoints(PointMode.points, Float32List.fromList(light), p..color = Colors.white.withValues(alpha: 0.35));
  }

  @override
  bool shouldRepaint(_Grain old) => false;
}

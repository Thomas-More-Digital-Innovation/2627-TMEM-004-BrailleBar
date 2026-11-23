import 'package:braillebar/paper.dart';
import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';

/// Shared page shell: wrap every screen's content in this.
class AppLayout extends StatelessWidget {
  const AppLayout({super.key, required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        toolbarHeight: 72,
        title: SvgPicture.asset('assets/LogoBrailleBar.svg', height: 30, semanticsLabel: 'BrailleBar',
            colorFilter: const ColorFilter.mode(ink, BlendMode.srcIn)),
        actions: [
          Padding(padding: const EdgeInsets.only(right: 24), child: Semantics(header: true, child: Text(title))),
        ],
      ),
      body: SafeArea(child: child),
    );
  }
}

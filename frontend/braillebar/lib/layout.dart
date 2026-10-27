import 'package:flutter/material.dart';

/// Shared page shell: wrap every screen's content in this.
class AppLayout extends StatelessWidget {
  const AppLayout({super.key, required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        backgroundColor: Theme.of(context).colorScheme.inversePrimary,
        title: Text(title),
      ),
      body: SafeArea(child: child),
    );
  }
}

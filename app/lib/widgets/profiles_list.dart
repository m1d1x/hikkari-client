import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../state/app_state.dart';
import 'profile_tile.dart';

/// Vertical list of [ProfileTile]s. Owns the "currently expanded" state
/// (only one tile open at a time, same as the Svelte version).
class ProfilesList extends StatefulWidget {
  const ProfilesList({super.key});

  @override
  State<ProfilesList> createState() => _ProfilesListState();
}

class _ProfilesListState extends State<ProfilesList> {
  String? _expanded;

  @override
  Widget build(BuildContext context) {
    final profiles = context.watch<AppState>().profiles;
    if (profiles.isEmpty) return const SizedBox.shrink();

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Column(
        children: [
          for (var i = 0; i < profiles.length; i++)
            ProfileTile(
              key: ValueKey(profiles[i].name),
              profile: profiles[i],
              expanded: _expanded == profiles[i].name,
              onToggleExpand: () => setState(() {
                _expanded =
                    _expanded == profiles[i].name ? null : profiles[i].name;
              }),
            ),
        ],
      ),
    );
  }
}

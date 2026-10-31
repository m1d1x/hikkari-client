import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/deep_links.dart';
import 'package:nekkobox/core/models.dart';

void main() {
  group('hikkari:// links', () {
    test('import carries a subscription or a server key', () {
      const sub = 'https://sub.hikkari.net/sub/abc_DEF-123';
      final link = DeepLink.parse(
        'hikkari://import?url=${Uri.encodeComponent(sub)}',
      );
      expect(link, isA<ImportLink>());
      expect((link! as ImportLink).link, sub);
      expect(
        DeepLink.parse('hikkari://import?url=vless%3A%2F%2Fid%40host%3A443'),
        isA<ImportLink>(),
      );
      expect(
        DeepLink.parse('hikkari://import?url=vpn%3A%2F%2Fabc123'),
        isA<ImportLink>(),
      );
      expect(ImportLink.supports('vpn://abc123'), isTrue);
      expect(ImportLink.supports('VPN://ABC123'), isTrue);
    });

    test('anything else is ignored', () {
      for (final raw in [
        'https://hikkari.net/pay',
        'hikkari://import',
        'hikkari://import?url=file%3A%2F%2F%2Fetc%2Fpasswd',
        'hikkari://import?url=javascript%3Aalert(1)',
        'hikkari://delete?url=https%3A%2F%2Fx',
        'not a link',
      ]) {
        expect(DeepLink.parse(raw), isNull, reason: raw);
      }
    });
  });

  group('renewal', () {
    SubscriptionInfo info(String page) => SubscriptionInfo(
      id: 's',
      title: 'Hikkari',
      uploadBytes: 0,
      downloadBytes: 0,
      totalBytes: 0,
      expiresAt: 0,
      updateIntervalSeconds: 0,
      webPageUrl: page,
      supportUrl: '',
    );

    test('only a hikkari subscription is renewed in the app', () {
      final ours = info('https://hikkari.net/pay#renew=tok123');
      expect(ours.renewUrl?.fragment, 'renew=tok123');
      expect(info('https://other-vpn.example/pay').renewUrl, isNull);
      expect(info('http://hikkari.net/pay#renew=x').renewUrl, isNull);
      expect(info('https://hikkari.net/').renewUrl, isNull);
      expect(info('').renewUrl, isNull);
    });
  });
}

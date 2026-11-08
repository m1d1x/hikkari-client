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
      for (final scheme in ['naive+https', 'naive+quic', 'hikkari+v1']) {
        final server = '$scheme://user:pass@proxy.example:443';
        final link = DeepLink.parse(
          'hikkari://import?url=${Uri.encodeComponent(server)}',
        );
        expect(link, isA<ImportLink>());
        expect((link! as ImportLink).link, server);
        expect(ImportLink.supports(server.toUpperCase()), isTrue);
      }
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

    test('isHikkari identifies hikkari subscriptions accurately', () {
      expect(info('https://hikkari.net/pay#renew=tok123').isHikkari, isTrue);
      expect(
        SubscriptionInfo(
          id: '1',
          title: 'hk_pelmen',
          uploadBytes: 0,
          downloadBytes: 0,
          totalBytes: 0,
          expiresAt: 0,
          updateIntervalSeconds: 0,
          webPageUrl: '',
          supportUrl: '',
        ).isHikkari,
        isTrue,
      );
      expect(
        SubscriptionInfo(
          id: '2',
          title: 'hikkari_123456',
          uploadBytes: 0,
          downloadBytes: 0,
          totalBytes: 0,
          expiresAt: 0,
          updateIntervalSeconds: 0,
          webPageUrl: '',
          supportUrl: '',
        ).isHikkari,
        isTrue,
      );
      expect(
        SubscriptionInfo(
          id: '3',
          title: 'pelmeska_s',
          uploadBytes: 0,
          downloadBytes: 0,
          totalBytes: 0,
          expiresAt: 0,
          updateIntervalSeconds: 0,
          webPageUrl: 'https://other-vpn.example',
          supportUrl: '',
        ).isHikkari,
        isFalse,
      );
    });

    test('protocolCaption names the protocol and hides an unknown one', () {
      expect(protocolCaption('vless-reality'), 'vless · reality');
      expect(protocolCaption('hysteria2'), 'hysteria2');
      expect(protocolCaption('other'), '');
      expect(protocolCaption(''), '');
    });

    test('formatProfileLabel formats cities into clean lowercase names', () {
      expect(formatProfileLabel('ФИНКА СТАРАЯ'), 'хельсинки-01');
      expect(formatProfileLabel('НИДЕРЛАНДСК'), 'амстердам-01');
      expect(formatProfileLabel('ГЕРМАНСК'), 'франкфурт-01');
      expect(formatProfileLabel('Hikkari ams1-1'), 'амстердам-01');
      expect(formatProfileLabel('Hikkari ams1-2'), 'амстердам-02');
      expect(formatProfileLabel('Hikkari hel1-1'), 'хельсинки-01');
      expect(formatProfileLabel('Hikkari eun17-2'), 'eun17-2');
      expect(formatProfileLabel('Hikkari Riga 001'), 'riga 001');
      expect(formatProfileLabel('Hikkari fra1-3'), 'франкфурт-03');
      expect(formatProfileLabel('хельсинки-02'), 'хельсинки-02');
      expect(formatProfileLabel('Custom-Server-1'), 'custom-server-1');
    });
  });
}

import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:nekkobox/core/routing_document.dart';
import 'package:nekkobox/core/nekkobox_core.dart';

class DocumentCore extends NekkoboxCore {
  String? method;
  Map<String, dynamic>? args;
  final Map<String, dynamic> document;
  DocumentCore(this.document);

  @override
  String rawCall(String method, [String argsJson = '']) {
    this.method = method;
    args = argsJson.isEmpty
        ? null
        : jsonDecode(argsJson) as Map<String, dynamic>;
    return jsonEncode({'ok': true, 'data': document});
  }
}

void main() {
  Map<String, dynamic> fixture() =>
      jsonDecode(
            File(
              '../engine/core/routing/testdata/document-v1.json',
            ).readAsStringSync(),
          )
          as Map<String, dynamic>;

  test('Go and Dart share the same lossless v1 document', () {
    final json = fixture();
    final document = RoutingDocument.fromJson(json);
    expect(document.toJson(), json);
    expect(
      document.sources.first.bypass.processes.keys,
      containsAll(['darwin', 'windows', 'linux']),
    );
    expect(() => document.sources.clear(), throwsUnsupportedError);
    expect(
      () => document.sources.first.bypass.processes['windows']!.clear(),
      throwsUnsupportedError,
    );
    expect(
      () => RoutingDocument.fromJson({...json, 'schemaVersion': 2}),
      throwsFormatException,
    );
    expect(
      () => RoutingDocument.fromJson({...json, 'mode': 'unknown'}),
      throwsFormatException,
    );
  });

  test('FFI reads the versioned document without native config', () async {
    final core = DocumentCore(fixture());
    final document = await core.routingDocument();
    expect(core.method, 'RoutingDocument');
    expect(document.toJson(), fixture());
  });
}

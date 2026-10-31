import Cocoa
import FlutterMacOS

@main
class AppDelegate: FlutterAppDelegate {
  override func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
    return true
  }

  override func applicationSupportsSecureRestorableState(_ app: NSApplication) -> Bool {
    return true
  }

  // hikkari:// links (Info.plist CFBundleURLTypes), the one the app was
  // started with included.
  override func application(_ application: NSApplication, open urls: [URL]) {
    for url in urls where url.scheme == "hikkari" {
      LinkChannel.shared.deliver(url.absoluteString)
    }
  }
}

/// The runner side of lib/core/deep_links.dart: links wait here until Dart
/// says `ready`, then go straight through with `open`.
final class LinkChannel {
  static let shared = LinkChannel()

  private var channel: FlutterMethodChannel?
  private var pending: [String] = []
  private var ready = false

  func attach(_ messenger: FlutterBinaryMessenger) {
    let channel = FlutterMethodChannel(name: "hikkari/links", binaryMessenger: messenger)
    channel.setMethodCallHandler { [weak self] call, result in
      guard let self, call.method == "ready" else {
        result(FlutterMethodNotImplemented)
        return
      }
      self.ready = true
      result(self.pending)
      self.pending.removeAll()
    }
    self.channel = channel
  }

  func deliver(_ link: String) {
    if ready, let channel {
      channel.invokeMethod("open", arguments: link)
    } else {
      pending.append(link)
    }
  }
}

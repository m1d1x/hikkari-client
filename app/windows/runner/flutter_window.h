#ifndef RUNNER_FLUTTER_WINDOW_H_
#define RUNNER_FLUTTER_WINDOW_H_

#include <flutter/dart_project.h>
#include <flutter/encodable_value.h>
#include <flutter/flutter_view_controller.h>
#include <flutter/method_channel.h>

#include <memory>
#include <string>
#include <vector>

#include "win32_window.h"

// A window that does nothing but host a Flutter view.
class FlutterWindow : public Win32Window {
 public:
  // Creates a new FlutterWindow hosting a Flutter view running |project|.
  explicit FlutterWindow(const flutter::DartProject& project);
  virtual ~FlutterWindow();

  // Hands a hikkari:// link to Dart (lib/core/deep_links.dart): held until
  // Dart says `ready`, sent with `open` after.
  void QueueLink(const std::string& link);

 protected:
  // Win32Window:
  bool OnCreate() override;
  void OnDestroy() override;
  LRESULT MessageHandler(HWND window, UINT const message, WPARAM const wparam,
                         LPARAM const lparam) noexcept override;

 private:
  // The project to run.
  flutter::DartProject project_;

  // The Flutter instance hosted by this window.
  std::unique_ptr<flutter::FlutterViewController> flutter_controller_;

  // The hikkari/links channel and the links Dart has not asked for yet.
  std::unique_ptr<flutter::MethodChannel<flutter::EncodableValue>> links_;
  std::vector<std::string> pending_links_;
  bool links_ready_ = false;
};

#endif  // RUNNER_FLUTTER_WINDOW_H_

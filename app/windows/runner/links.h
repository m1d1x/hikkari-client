#ifndef RUNNER_LINKS_H_
#define RUNNER_LINKS_H_

#include <windows.h>

#include <string>
#include <vector>

// hikkari:// links (lib/core/deep_links.dart). The launcher passes a link as
// the GUI's argument; a GUI started while another one's window is open hands
// its link over with WM_COPYDATA and exits, so there is one window and one
// engine per session.

// Held while a window is open. The launcher reads it too
// (engine/cmd/hikkari-launcher), so the names must match.
#ifdef _DEBUG
constexpr wchar_t kInstanceMutex[] = L"Local\\hikkari-gui-debug";
#else
constexpr wchar_t kInstanceMutex[] = L"Local\\hikkari-gui";
#endif

constexpr wchar_t kWindowTitle[] = L"hikkari";
constexpr wchar_t kWindowClass[] = L"FLUTTER_RUNNER_WIN32_WINDOW";

// WM_COPYDATA tag of a forwarded link; the payload is the link in UTF-8.
constexpr ULONG_PTR kLinkCopyData = 0x484B4C4B;  // 'HKLK'

// The hikkari:// link among the arguments, or "".
inline std::string LinkArgument(const std::vector<std::string>& args) {
  for (const auto& arg : args) {
    if (arg.rfind("hikkari:", 0) == 0) return arg;
  }
  return "";
}

// Brings the open window forward and hands it link (if any).
inline void ForwardToRunningInstance(const std::string& link) {
  HWND window = ::FindWindowW(kWindowClass, kWindowTitle);
  if (!window) return;
  DWORD pid = 0;
  ::GetWindowThreadProcessId(window, &pid);
  // This process was started by the user's click and may raise windows; let
  // the running one raise itself too when its Dart side shows the dialog.
  ::AllowSetForegroundWindow(pid);
  if (::IsIconic(window)) ::ShowWindow(window, SW_RESTORE);
  ::SetForegroundWindow(window);
  if (link.empty()) return;
  COPYDATASTRUCT data{};
  data.dwData = kLinkCopyData;
  data.cbData = static_cast<DWORD>(link.size());
  data.lpData = const_cast<char*>(link.data());
  DWORD_PTR ignored = 0;
  ::SendMessageTimeoutW(window, WM_COPYDATA, 0, reinterpret_cast<LPARAM>(&data),
                        SMTO_ABORTIFHUNG, 5000, &ignored);
}

#endif  // RUNNER_LINKS_H_

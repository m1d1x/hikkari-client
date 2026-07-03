// Package applist enumerates user-visible applications on Windows using
// native Win32 APIs: CreateToolhelp32Snapshot for running processes and
// GetFileVersionInfo for friendly names. No PowerShell, no COM, no runtime
// dependencies.
package applist

// AppEntry is a single discovered application.
type AppEntry struct {
	Name    string `json:"name"`              // friendly name (ProductName / FileDescription / exe stem)
	Exe     string `json:"exe"`               // filename: "Telegram.exe"
	Path    string `json:"path"`              // full path: "C:\Users\...\Telegram.exe"
	Running bool   `json:"running"`           // true if currently has a visible window
	Icon    string `json:"icon,omitempty"`    // base64-encoded PNG icon
}

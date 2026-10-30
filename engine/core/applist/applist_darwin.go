package applist

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// List returns applications for the routing picker, deduplicated by
// executable name.
//
// Exe is the bare binary name inside Contents/MacOS — that is what sing-box's
// process_name compares against on macOS (filepath.Base of the process path),
// not the bundle's display name.
//
// all=false is the short list: GUI apps installed in /Applications and
// ~/Applications plus any running top-level .app bundle outside /System.
// Unlike Windows, installed-but-not-running apps are included too, since a
// Mac user expects to pick from the Applications folder. all=true adds every
// running process, helpers and daemons included — Chrome's traffic, for
// one, comes from "Google Chrome Helper", not "Google Chrome".
func List(all bool) []AppEntry {
	procs := snapshotProcesses()
	running := map[string]bool{}
	for _, p := range procs {
		running[p] = true
	}

	seen := map[string]bool{}
	var result []AppEntry
	add := func(e AppEntry) {
		if e.Exe == "" || seen[e.Exe] {
			return
		}
		seen[e.Exe] = true
		result = append(result, e)
	}

	// Running bundles first, so an app that is both installed and running
	// keeps the path it actually runs from.
	for _, p := range procs {
		bundle := topLevelBundle(p)
		if bundle == "" || strings.HasPrefix(bundle, "/System/") {
			continue
		}
		m := metaFor(bundle)
		if filepath.Base(p) != m.exe {
			continue // a secondary binary of the bundle, not its main one
		}
		add(AppEntry{Name: m.name, Exe: m.exe, Path: p, Running: true, Icon: m.icon})
	}

	for _, bundle := range installedBundles() {
		m := metaFor(bundle)
		if m.exe == "" {
			continue
		}
		path := filepath.Join(bundle, "Contents", "MacOS", m.exe)
		add(AppEntry{Name: m.name, Exe: m.exe, Path: path, Running: running[path], Icon: m.icon})
	}

	if all {
		for _, p := range procs {
			exe := filepath.Base(p)
			name := exe
			var icon string
			if bundle := innermostBundle(p); bundle != "" {
				if m := metaFor(bundle); m.exe == exe {
					name, icon = m.name, m.icon
				}
			}
			add(AppEntry{Name: name, Exe: exe, Path: p, Running: true, Icon: icon})
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

// snapshotProcesses returns the executable path of every process. ps's comm
// column on macOS is the full path for anything launched from disk, and it
// needs no entitlement, unlike proc_pidpath against other users' processes.
func snapshotProcesses() []string {
	out, err := exec.Command("/bin/ps", "-axo", "pid=,comm=").Output()
	if err != nil {
		return nil
	}
	var paths []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		i := strings.IndexByte(line, ' ')
		if i < 0 {
			continue
		}
		if _, err := strconv.Atoi(line[:i]); err != nil {
			continue
		}
		p := strings.TrimSpace(line[i+1:])
		if strings.HasPrefix(p, "/") {
			paths = append(paths, p)
		}
	}
	return paths
}

// topLevelBundle returns the .app a process is the main binary of
// (X.app/Contents/MacOS/bin), or "" for anything else — including helper
// apps nested inside another bundle's Frameworks or Helpers.
func topLevelBundle(p string) string {
	i := strings.Index(p, ".app/Contents/MacOS/")
	if i < 0 {
		return ""
	}
	bundle := p[:i+len(".app")]
	if strings.Contains(p[i+len(".app/Contents/MacOS/"):], "/") {
		return ""
	}
	if strings.Contains(bundle[:strings.LastIndex(bundle, "/")], ".app/") {
		return ""
	}
	return bundle
}

// innermostBundle returns the closest enclosing .app of p, nested or not.
func innermostBundle(p string) string {
	i := strings.LastIndex(p, ".app/Contents/MacOS/")
	if i < 0 {
		return ""
	}
	return p[:i+len(".app")]
}

func installedBundles() []string {
	dirs := []string{"/Applications", "/Applications/Utilities", "/System/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	var bundles []string
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".app") {
				bundles = append(bundles, filepath.Join(d, e.Name()))
			}
		}
	}
	return bundles
}

// bundleMeta is what costs real work per bundle: an Info.plist decode and an
// icns → PNG conversion. Neither changes while the bundle sits on disk and the
// picker polls every few seconds, so both are cached by bundle path for the
// lifetime of the process.
type bundleMeta struct {
	name string
	exe  string
	icon string
}

var (
	metaMu    sync.Mutex
	metaCache = map[string]bundleMeta{}
)

func metaFor(bundle string) bundleMeta {
	metaMu.Lock()
	m, ok := metaCache[bundle]
	metaMu.Unlock()
	if ok {
		return m
	}

	info := readInfoPlist(bundle)
	m.exe = info["CFBundleExecutable"]
	m.name = info["CFBundleDisplayName"]
	if m.name == "" {
		m.name = info["CFBundleName"]
	}
	if m.name == "" {
		m.name = strings.TrimSuffix(filepath.Base(bundle), ".app")
	}
	if m.exe != "" {
		m.icon = iconBase64(bundle, info["CFBundleIconFile"])
	}

	metaMu.Lock()
	metaCache[bundle] = m
	metaMu.Unlock()
	return m
}

// readInfoPlist decodes the string keys of a bundle's Info.plist. plutil
// handles both the XML and binary encodings, so no plist parser is vendored.
func readInfoPlist(bundle string) map[string]string {
	out, err := exec.Command("/usr/bin/plutil", "-convert", "json", "-o", "-",
		filepath.Join(bundle, "Contents", "Info.plist")).Output()
	if err != nil {
		return nil
	}
	var raw map[string]interface{}
	if json.Unmarshal(out, &raw) != nil {
		return nil
	}
	res := map[string]string{}
	for k, v := range raw {
		if s, ok := v.(string); ok {
			res[k] = s
		}
	}
	return res
}

// iconBase64 converts the bundle's icns to a 32px PNG with sips. Bundles that
// only ship an asset catalog (no CFBundleIconFile) get no icon.
func iconBase64(bundle, iconFile string) string {
	if iconFile == "" {
		return ""
	}
	if filepath.Ext(iconFile) == "" {
		iconFile += ".icns"
	}
	src := filepath.Join(bundle, "Contents", "Resources", iconFile)
	if _, err := os.Stat(src); err != nil {
		return ""
	}
	tmp, err := os.CreateTemp("", "nekkobox-icon-*.png")
	if err != nil {
		return ""
	}
	dst := tmp.Name()
	tmp.Close()
	defer os.Remove(dst)

	if exec.Command("/usr/bin/sips", "-s", "format", "png", "-Z", "32", src, "--out", dst).Run() != nil {
		return ""
	}
	data, err := os.ReadFile(dst)
	if err != nil || len(data) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}

package manager

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jeefyang/fnos_daili/internal/spec"
)

// Installed apps keep their entry icon under <install dir>/ui/images (that is
// where our generated packages and watchcow both put it), so an app that the
// manager does not own can still be listed with its real icon.
const (
	iconMaxBytes = 1024 * 1024
	iconDirRel   = "ui/images"
)

// appIconDirs lists the candidate install dirs for one app. InstallRoots is
// overridden in tests; on a real host the roots are globs so every mounted
// volume is covered.
func (s *Service) appIconDirs(appname string) []string {
	roots := s.cfg.InstallRoots
	if len(roots) == 0 {
		roots = []string{"/vol*/@appcenter", "/var/apps"}
	}
	var dirs []string
	for _, root := range roots {
		if strings.ContainsAny(root, "*?[") {
			matches, err := filepath.Glob(root)
			if err != nil {
				continue
			}
			for _, m := range matches {
				dirs = append(dirs, filepath.Join(m, appname))
			}
			continue
		}
		dirs = append(dirs, filepath.Join(root, appname))
	}
	return dirs
}

// appIcon returns the best icon found for an installed app, or nil.
func (s *Service) appIcon(appname string) []byte {
	for _, dir := range s.appIconDirs(appname) {
		if data := readIconFromDir(dir); data != nil {
			return data
		}
	}
	return nil
}

func readIconFromDir(dir string) []byte {
	images, _ := filepath.Glob(filepath.Join(dir, iconDirRel, "*.png"))
	// Prefer the 256px variant; entry icons are named icon_256.png (ours) or
	// icon_<entry>_256.png (watchcow).
	sort.Slice(images, func(i, j int) bool { return iconScore(images[i]) > iconScore(images[j]) })
	for _, p := range images {
		if data := readIconFile(p); data != nil {
			return data
		}
	}
	for _, name := range []string{"ICON_256.PNG", "ICON.PNG"} {
		if data := readIconFile(filepath.Join(dir, name)); data != nil {
			return data
		}
	}
	return nil
}

func iconScore(path string) int {
	name := strings.ToLower(filepath.Base(path))
	score := 0
	if strings.Contains(name, "256") {
		score += 20
	}
	if strings.Contains(name, "icon") {
		score += 10
	}
	return score
}

func readIconFile(path string) []byte {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() <= 0 || info.Size() > iconMaxBytes {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	return data
}

// handleAppIcon serves the icon of an installed app by appname, which is how
// the unmanaged list shows real icons.
func (s *Service) handleAppIcon(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("appname")
	if err := spec.ValidateAppName(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_appname", "appname 不合法")
		return
	}
	data := s.appIcon(name)
	if len(data) == 0 {
		writeError(w, http.StatusNotFound, "no_icon", "没有找到该应用的图标")
		return
	}
	serveIcon(w, data)
}

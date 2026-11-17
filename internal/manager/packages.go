package manager

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handlePackageZip streams the generated fpk files of the selected targets as
// one zip, so a multi-selection download does not fire N browser downloads.
func (s *Service) handlePackageZip(w http.ResponseWriter, r *http.Request) {
	ids := splitIDList(r.URL.Query().Get("ids"))
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "missing_ids", "先用复选框选中要下载的应用")
		return
	}
	root := filepath.Join(s.cfg.DataDir, "packages")
	type entry struct {
		name string
		path string
	}
	var entries []entry
	used := map[string]bool{}
	for _, id := range ids {
		t, ok := s.getTarget(id)
		if !ok || t.Runtime.PackagePath == "" {
			continue
		}
		path := t.Runtime.PackagePath
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			continue
		}
		name := filepath.Base(path)
		if used[name] {
			base := strings.TrimSuffix(name, ".fpk")
			for n := 2; ; n++ {
				candidate := fmt.Sprintf("%s-%d.fpk", base, n)
				if !used[candidate] {
					name = candidate
					break
				}
			}
		}
		used[name] = true
		entries = append(entries, entry{name: name, path: path})
	}
	if len(entries) == 0 {
		writeError(w, http.StatusNotFound, "no_package", "选中的应用还没有生成过安装包")
		return
	}
	stamp := s.now().Format("20060102-150405")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "daili-packages-"+stamp+".zip"))
	w.WriteHeader(http.StatusOK)
	zw := zip.NewWriter(w)
	for _, e := range entries {
		fw, err := zw.Create(e.name)
		if err != nil {
			break
		}
		f, err := os.Open(e.path)
		if err != nil {
			break
		}
		_, copyErr := io.Copy(fw, f)
		f.Close()
		if copyErr != nil {
			break
		}
	}
	_ = zw.Close()
}

package manager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"strings"
	"testing"
)

func readFPKMember(t *testing.T, path, member string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimPrefix(h.Name, "./") == member {
			data, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	t.Fatalf("member %q not found", member)
	return nil
}

func readAppTgzMember(t *testing.T, appTgz []byte, member string) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(appTgz))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimPrefix(h.Name, "./") == member {
			data, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	t.Fatalf("app.tgz member %q not found", member)
	return nil
}

func TestBuildPackageUsesCustomIcon(t *testing.T) {
	s, _ := newTestService(t)
	in := input()
	in.IconData = testPNGDataURL(t, 200, 120)
	got, err := s.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Spec.HasIcon {
		t.Fatal("has_icon = false after upload")
	}
	s.mu.Lock()
	tgt := s.findLocked(got.Spec.ID)
	s.mu.Unlock()
	if tgt == nil {
		t.Fatal("target disappeared")
	}
	path, _, err := s.buildPackage(*tgt, "0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	appTgz := readFPKMember(t, path, "app.tgz")
	icon256 := readAppTgzMember(t, appTgz, "ui/images/icon_256.png")
	if !bytes.Equal(icon256, tgt.CustomIcon256) {
		t.Fatalf("package icon (len=%d) does not match uploaded icon (len=%d)", len(icon256), len(tgt.CustomIcon256))
	}
}

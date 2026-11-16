package fpk

import (
	"bytes"
	"testing"
)

func TestBuildManagerVerifies(t *testing.T) {
	var buf bytes.Buffer
	built, err := BuildManager(&buf, ManagerOptions{
		ManagerBinary: elf64(62, false),
		ProxyBinary:   elf64(62, false),
		Icon64:        pngBytes(t, 64),
		Icon256:       pngBytes(t, 256),
		Version:       "1.2.3",
		Platform:      "x86",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyManager(buf.Bytes())
	if err != nil {
		t.Fatalf("VerifyManager() error: %v", err)
	}
	if got.AppName != "daili.manager" || got.Version != "1.2.3" {
		t.Fatalf("result = %+v", got)
	}
	if got.SHA256 != built.SHA256 || got.Size != built.Size {
		t.Fatalf("digest = %+v built = %+v", got, built)
	}
	if got.ManagerSize == 0 || got.ProxySize == 0 {
		t.Fatalf("sizes = %+v", got)
	}
}

func TestVerifyManagerRejectsProxyPrivilege(t *testing.T) {
	var buf bytes.Buffer
	if _, err := BuildManager(&buf, ManagerOptions{
		ManagerBinary: elf64(62, false),
		ProxyBinary:   elf64(62, false),
		Icon64:        pngBytes(t, 64),
		Icon256:       pngBytes(t, 256),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(buf.Bytes()); err == nil {
		t.Fatal("Verify() accepted a manager package")
	}
}

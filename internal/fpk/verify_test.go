package fpk

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func elf64(machine uint16, interp bool) []byte {
	b := make([]byte, 64+56)
	copy(b[:4], []byte{0x7f, 'E', 'L', 'F'})
	b[4] = 2 // ELFCLASS64
	b[5] = 1 // ELFDATA2LSB
	b[6] = 1 // EV_CURRENT
	binary.LittleEndian.PutUint16(b[16:18], 2) // ET_EXEC
	binary.LittleEndian.PutUint16(b[18:20], machine)
	binary.LittleEndian.PutUint32(b[20:24], 1)
	binary.LittleEndian.PutUint64(b[32:40], 64)
	binary.LittleEndian.PutUint16(b[52:54], 64)
	binary.LittleEndian.PutUint16(b[54:56], 56)
	binary.LittleEndian.PutUint16(b[56:58], 1)
	ptype := uint32(1) // PT_LOAD
	if interp {
		ptype = 3 // PT_INTERP
	}
	binary.LittleEndian.PutUint32(b[64:68], ptype)
	binary.LittleEndian.PutUint32(b[68:72], 5)
	return b
}

func TestVerifyAcceptsBuiltPackage(t *testing.T) {
	opts := testOptions(t)
	opts.Assets.ProxyBinary = elf64(62, false)
	var buf bytes.Buffer
	built, err := BuildProxy(&buf, opts)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify(buf.Bytes())
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if got.AppName != "proxy.home.9f3a" || got.Version != "1.0.0" || got.Platform != "x86" {
		t.Fatalf("result identity = %+v", got)
	}
	if got.SHA256 != built.SHA256 || got.Size != built.Size {
		t.Fatalf("result digest = %+v, built = %+v", got, built)
	}
	if got.DesktopAppLaunchName != "proxy.home.9f3a.main" {
		t.Fatalf("launch name = %q", got.DesktopAppLaunchName)
	}
	if got.ProxySize != int64(len(elf64(62, false))) {
		t.Fatalf("proxy size = %d", got.ProxySize)
	}
}

func TestVerifyRejectsShellScript(t *testing.T) {
	var buf bytes.Buffer
	if _, err := BuildProxy(&buf, testOptions(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(buf.Bytes()); err == nil {
		t.Fatal("Verify() accepted a non-ELF proxy")
	}
}

func TestVerifyRejectsDynamicProxy(t *testing.T) {
	opts := testOptions(t)
	opts.Assets.ProxyBinary = elf64(62, true)
	var buf bytes.Buffer
	if _, err := BuildProxy(&buf, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(buf.Bytes()); err == nil {
		t.Fatal("Verify() accepted a dynamically linked proxy")
	}
}

func TestVerifyRejectsWrongMachine(t *testing.T) {
	opts := testOptions(t)
	opts.Assets.ProxyBinary = elf64(183, false) // AArch64 in an x86 package
	var buf bytes.Buffer
	if _, err := BuildProxy(&buf, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(buf.Bytes()); err == nil {
		t.Fatal("Verify() accepted a mismatched ELF machine")
	}
}

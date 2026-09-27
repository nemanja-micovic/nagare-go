package paths

import (
	"path/filepath"
	"testing"
)

func TestDataHonoursOverride(t *testing.T) {
	t.Setenv(DataDirEnv, "/tmp/nagare-demo-data")
	if got := Data(); got != "/tmp/nagare-demo-data" {
		t.Errorf("Data() = %q, want the override", got)
	}
}

func TestDataDefault(t *testing.T) {
	t.Setenv(DataDirEnv, "")
	t.Setenv("HOME", "/home/someone")
	if got, want := Data(), filepath.Join("/home/someone", ".local", "share", "nagare"); got != want {
		t.Errorf("Data() = %q, want %q", got, want)
	}
}

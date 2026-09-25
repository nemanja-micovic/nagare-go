package fsutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Concurrent writers of one file must each land whole: the file is always
// one writer's complete content, never a mix.
func TestAtomicWriteConcurrentWritersNeverCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload, _ := json.Marshal(map[string]string{"writer": strings.Repeat(string(rune('a'+i%26)), 4096)})
			for j := 0; j < 20; j++ {
				if err := AtomicWrite(path, payload, 0644); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]string
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("file corrupted by concurrent writers: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %d entries", len(entries))
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0644 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

package search

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSearchEngine(t *testing.T) {
	tempDir := t.TempDir()

	// Create test files
	f1 := filepath.Join(tempDir, "anime_movie.mkv")
	f2 := filepath.Join(tempDir, "minecraft_skin.png")
	f3 := filepath.Join(tempDir, "notes.txt")
	subDir := filepath.Join(tempDir, "subfolder")
	_ = os.MkdirAll(subDir, 0755)
	f4 := filepath.Join(subDir, "secret_plan.pdf")

	_ = os.WriteFile(f1, []byte("large movie content here"), 0644)
	_ = os.WriteFile(f2, []byte("png image pixel data"), 0644)
	_ = os.WriteFile(f3, []byte("important password: chest_vault_123"), 0644)
	_ = os.WriteFile(f4, []byte("pdf document data"), 0644)

	t.Run("Fuzzy match filename", func(t *testing.T) {
		eng := New(SearchOptions{
			RootPath: tempDir,
			Pattern:  "mine",
		})
		res, err := eng.Search()
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(res) == 0 {
			t.Fatalf("Expected match for 'mine', got 0")
		}
		if res[0].Name != "minecraft_skin.png" {
			t.Errorf("Expected 'minecraft_skin.png', got %s", res[0].Name)
		}
	})

	t.Run("Filter by category/type", func(t *testing.T) {
		eng := New(SearchOptions{
			RootPath: tempDir,
			Type:     "video",
		})
		res, err := eng.Search()
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(res) != 1 || res[0].Name != "anime_movie.mkv" {
			t.Fatalf("Expected 1 video match, got %d", len(res))
		}
	})

	t.Run("Content grep search", func(t *testing.T) {
		eng := New(SearchOptions{
			RootPath:     tempDir,
			ContentQuery: "chest_vault",
		})
		res, err := eng.Search()
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if len(res) != 1 || res[0].Name != "notes.txt" {
			t.Fatalf("Expected content match in notes.txt, got %d", len(res))
		}
		if res[0].ContentLineNo != 1 {
			t.Errorf("Expected line 1, got %d", res[0].ContentLineNo)
		}
	})

	t.Run("Content search skips binary files", func(t *testing.T) {
		// Binary file whose header contains the query bytes but also a NUL
		// byte. It must NOT surface a raw snippet (previously it leaked
		// raw bytes and corrupted terminal output).
		binFile := filepath.Join(tempDir, "data.wav")
		_ = os.WriteFile(binFile, []byte("RIFF\x00\x00chest_vault\x00WAVE"), 0644)

		eng := New(SearchOptions{
			RootPath:     tempDir,
			ContentQuery: "chest_vault",
		})
		res, err := eng.Search()
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		for _, m := range res {
			if m.Name == "data.wav" {
				t.Fatal("binary file leaked into content search results")
			}
		}
		if len(res) != 1 || res[0].Name != "notes.txt" {
			t.Fatalf("Expected only notes.txt to match, got %d", len(res))
		}
	})

	t.Run("Snippet strips control characters", func(t *testing.T) {
		// ESC (0x1B) and NUL are dropped; printable text (including the
		// literal "[31m" that followed the ESC) is preserved, so no control
		// byte can reach the terminal.
		if got := sanitizeSnippet([]byte("\x1b[31mAB\x00C\tDE\x1b[0m")); got != "[31mABC\tDE[0m" {
			t.Fatalf("sanitizeSnippet = %q, want %q", got, "[31mABC\tDE[0m")
		}
	})
}

func TestMatchesSizeCondition(t *testing.T) {
	if !matchesSizeCondition(1024*1024*5, ">1MB") {
		t.Error("Expected 5MB to match >1MB")
	}
	if matchesSizeCondition(100, ">1KB") {
		t.Error("Expected 100 bytes not to match >1KB")
	}
	if !matchesSizeCondition(500, "<1KB") {
		t.Error("Expected 500 bytes to match <1KB")
	}
}

func TestMatchesDateCondition(t *testing.T) {
	now := time.Now()
	oldTime := now.AddDate(0, 0, -60) // 60 days ago

	if !matchesDateCondition(oldTime, "<30d") { // older than 30d
		t.Error("Expected 60 days ago to match <30d")
	}
}

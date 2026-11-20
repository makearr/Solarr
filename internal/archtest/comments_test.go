package archtest

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// minimumLongBlock is the length of a comment block that the one-line comment rule (documentation/coding-standards.md) forbids.
const minimumLongBlock = 4

// TestCommentBlocks_NoFileGrowsItsLongBlocks holds each file to the long comment blocks it has; shrink a file's
// allowance in comment_allowlist.txt when a block moves to documentation/.
func TestCommentBlocks_NoFileGrowsItsLongBlocks(t *testing.T) {
	root := repoRoot(t)
	allowed := readCommentAllowlist(t, filepath.Join(root, "internal", "archtest", "comment_allowlist.txt"))
	for _, directory := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(fullPath string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(fullPath, ".go") || strings.HasSuffix(fullPath, "_test.go") || strings.Contains(fullPath, "/gen/") {
				return err
			}
			relative, _ := filepath.Rel(root, fullPath)
			relative = filepath.ToSlash(relative)
			count, generated := longCommentBlocks(t, fullPath)
			if !generated && count > allowed[relative] {
				t.Errorf("%s has %d comment blocks of %d or more lines, %d allowed: one line saying why, longer text goes to documentation/", relative, count, minimumLongBlock, allowed[relative])
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func longCommentBlocks(t *testing.T, fullPath string) (count int, generated bool) {
	t.Helper()
	file, err := os.Open(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	run := 0
	end := func() {
		if run >= minimumLongBlock {
			count++
		}
		run = 0
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "Code generated") && strings.HasPrefix(line, "//") {
			return 0, true
		}
		if strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "//go:") && !strings.HasPrefix(line, "//nolint") {
			run++
			continue
		}
		end()
	}
	end()
	return count, false
}

func readCommentAllowlist(t *testing.T, name string) map[string]int {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading the comment allowlist: %v", err)
	}
	allowed := map[string]int{}
	for line := range strings.Lines(string(content)) {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		count, err := strconv.Atoi(fields[1])
		if err != nil {
			t.Fatalf("comment allowlist line %q: %v", line, err)
		}
		allowed[fields[0]] = count
	}
	return allowed
}

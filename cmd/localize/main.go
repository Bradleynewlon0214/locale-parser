package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/alexaandru/go-sitter-forest/vue"
	sitter "github.com/alexaandru/go-tree-sitter-bare"
)

var parser *sitter.Parser
var lang *sitter.Language

func init() {
	parser = sitter.NewParser()
	lang = sitter.NewLanguage(vue.GetLanguage())
	parser.SetLanguage(lang)
}

// Match represents a text node found in a Vue template
type Match struct {
	Line      int
	File      string
	Text      string
	StartByte uint
	EndByte   uint
	Key       string
}

func (m *Match) Print() {
	fmt.Printf("%s:%d  %q\n", m.File, m.Line, m.Text)
}

// PrintDiff shows a diff-style preview for dry-run mode
func (m *Match) PrintDiff() {
	fmt.Printf("%s:%d\n", m.File, m.Line)
	fmt.Printf("  - %s\n", m.Text)
	fmt.Printf("  + {{ $t('%s') }}\n\n", m.Key)
}

// isWhitespaceOnly checks if a string contains only whitespace characters
func isWhitespaceOnly(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// slugify converts text to a kebab-case slug, truncated at maxLen chars on word boundary
func slugify(text string, maxLen int) string {
	// Lowercase
	text = strings.ToLower(text)

	// Replace non-alphanumeric chars with hyphens
	reg := regexp.MustCompile(`[^a-z0-9]+`)
	text = reg.ReplaceAllString(text, "-")

	// Trim leading/trailing hyphens
	text = strings.Trim(text, "-")

	// Truncate at word boundary (hyphen) if too long
	if len(text) > maxLen {
		text = text[:maxLen]
		// Find last hyphen to truncate at word boundary
		lastHyphen := strings.LastIndex(text, "-")
		if lastHyphen > 0 {
			text = text[:lastHyphen]
		}
	}

	return text
}

// generateKey creates a translation key from file path and text
// Format: path.segments.text-slug
// Handles duplicate keys by appending -2, -3, etc.
func generateKey(filePath, basePath, text string, maxSlug int, keyCounter map[string]int) string {
	// Get relative path from base
	relPath, err := filepath.Rel(basePath, filePath)
	if err != nil {
		relPath = filePath
	}

	// Remove .vue extension
	relPath = strings.TrimSuffix(relPath, ".vue")

	// Lowercase
	relPath = strings.ToLower(relPath)

	// Replace path separators with dots
	relPath = strings.ReplaceAll(relPath, string(filepath.Separator), ".")

	// Generate slug from text
	slug := slugify(text, maxSlug)

	// Combine path and slug
	key := relPath + "." + slug

	// Handle duplicates
	keyCounter[key]++
	count := keyCounter[key]
	if count > 1 {
		key = fmt.Sprintf("%s-%d", key, count)
	}

	return key
}

// replaceInFile performs byte-level replacements on file content
// Matches must be sorted by StartByte in DESCENDING order before calling
func replaceInFile(content []byte, matches []Match) []byte {
	for _, m := range matches {
		replacement := []byte(fmt.Sprintf("{{ $t('%s') }}", m.Key))
		newContent := make([]byte, 0, len(content)-int(m.EndByte-m.StartByte)+len(replacement))
		newContent = append(newContent, content[:m.StartByte]...)
		newContent = append(newContent, replacement...)
		newContent = append(newContent, content[m.EndByte:]...)
		content = newContent
	}
	return content
}

// writeLocaleFile writes or merges translations into a JSON locale file
func writeLocaleFile(path string, translations map[string]string) error {
	existing := make(map[string]string)

	// Try to read existing file
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("failed to parse existing locale file: %w", err)
		}
	}

	// Merge new translations (don't overwrite existing)
	for key, value := range translations {
		if _, exists := existing[key]; !exists {
			existing[key] = value
		}
	}

	// Sort keys for consistent output
	keys := make([]string, 0, len(existing))
	for k := range existing {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build ordered map for output
	ordered := make(map[string]string)
	for _, k := range keys {
		ordered[k] = existing[k]
	}

	// Write JSON with indentation
	output, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal locale file: %w", err)
	}

	if err := os.WriteFile(path, output, 0644); err != nil {
		return fmt.Errorf("failed to write locale file: %w", err)
	}

	return nil
}

func worker(id int, jobs <-chan string, results chan<- []Match) {
	for j := range jobs {
		m, _ := parse(j, "", 30, nil)
		results <- m
	}
}

func getTemplate(tree *sitter.Tree, content []byte) *sitter.Node {
	q, err := sitter.NewQuery(lang, []byte(`(template_element) @template`))
	if err != nil {
		return nil
	}

	qc := sitter.NewQueryCursor()
	matches := qc.Matches(q, tree.RootNode(), content)

	m := matches.Next()
	if m == nil {
		return nil
	}
	if len(m.Captures) == 0 {
		return nil
	}
	node := m.Captures[0].Node
	return &node
}

func getText(tree *sitter.Tree, content []byte) sitter.QueryMatches {
	templateNode := getTemplate(tree, content)
	if templateNode == nil {
		return sitter.QueryMatches{}
	}

	q, err := sitter.NewQuery(lang, []byte(`(text) @text`))
	if err != nil {
		return sitter.QueryMatches{}
	}

	qc := sitter.NewQueryCursor()
	return qc.Matches(q, *templateNode, content)
}

func parse(path string, basePath string, maxSlug int, keyCounter map[string]int) ([]Match, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("Unable to read %s: %v\n", path, err)
		return nil, errors.New("Unable to read file")
	}

	tree, err := parser.ParseString(context.Background(), nil, content)
	if err != nil {
		return nil, errors.New("Unable to parse file")
	}
	defer tree.Close()

	matches := getText(tree, content)

	var results []Match
	for {
		m := matches.Next()
		if m == nil {
			break
		}
		for _, capture := range m.Captures {
			node := capture.Node
			text := node.Content(content)

			// Skip whitespace-only text nodes
			if isWhitespaceOnly(text) {
				continue
			}

			line := int(node.StartPoint().Row) + 1
			startByte := node.StartByte()
			endByte := node.EndByte()

			// Generate key if keyCounter is provided (replace mode)
			var key string
			if keyCounter != nil {
				key = generateKey(path, basePath, text, maxSlug, keyCounter)
			}

			results = append(results, Match{
				File:      path,
				Line:      line,
				Text:      text,
				StartByte: startByte,
				EndByte:   endByte,
				Key:       key,
			})
		}
	}
	return results, nil
}

func walk(path string, basePath string, maxSlug int, keyCounter map[string]int, results *[]Match) {
	entries, err := os.ReadDir(path)
	if err != nil {
		fmt.Printf("Unable to access %s directory\n", path)
		return
	}

	for _, entry := range entries {
		fullPath := filepath.Join(path, entry.Name())
		if entry.IsDir() {
			walk(fullPath, basePath, maxSlug, keyCounter, results)
		} else {
			if strings.HasSuffix(entry.Name(), ".vue") {
				matches, _ := parse(fullPath, basePath, maxSlug, keyCounter)
				for _, match := range matches {
					*results = append(*results, match)
				}
			}
		}
	}
}

func isDir(path string) {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
		return
	}

	if !info.IsDir() {
		fmt.Fprintln(os.Stderr, "error: path must be a directory")
		os.Exit(1)
		return
	}
}

func main() {
	path := flag.String("path", "", "path to file or directory")
	replace := flag.Bool("replace", false, "enable replacement mode (dry-run by default)")
	write := flag.Bool("write", false, "apply changes to files (use with -replace)")
	output := flag.String("output", "en.json", "path for generated locale JSON file")
	maxSlug := flag.Int("max-slug", 30, "maximum slug length in characters")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s -path ./src                     # Find un-localized text\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -path ./src -replace            # Dry-run replacement preview\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -path ./src -replace -write     # Apply replacements\n", os.Args[0])
	}

	flag.Parse()

	if *path == "" {
		fmt.Fprintln(os.Stderr, "error: -path is required")
		flag.Usage()
		os.Exit(1)
	}

	isDir(*path)

	// Get absolute path for consistent key generation
	absPath, err := filepath.Abs(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: unable to resolve path: %v\n", err)
		os.Exit(1)
	}

	var results []Match
	keyCounter := make(map[string]int)

	walk(absPath, absPath, *maxSlug, keyCounter, &results)

	if len(results) == 0 {
		fmt.Println("No un-localized text found.")
		os.Exit(0)
	}

	if !*replace {
		// Original behavior: just list matches
		fmt.Println("Encountered un-localized text!")
		for _, m := range results {
			m.Print()
		}
		os.Exit(1)
	}

	// Replace mode
	if !*write {
		// Dry-run: show preview
		fmt.Println("=== DRY RUN ===\n")
		for _, m := range results {
			m.PrintDiff()
		}

		// Count files affected
		fileSet := make(map[string]bool)
		for _, m := range results {
			fileSet[m.File] = true
		}

		fmt.Println("---")
		fmt.Printf("Summary:\n")
		fmt.Printf("  %d replacements across %d files\n", len(results), len(fileSet))
		fmt.Printf("  %d new keys for %s\n", len(results), *output)
		fmt.Printf("\nRun with -write to apply changes.\n")
		os.Exit(0)
	}

	// Write mode: apply changes
	// Group matches by file
	fileMatches := make(map[string][]Match)
	for _, m := range results {
		fileMatches[m.File] = append(fileMatches[m.File], m)
	}

	// Build translations map
	translations := make(map[string]string)
	for _, m := range results {
		translations[m.Key] = m.Text
	}

	// Process each file
	for filePath, matches := range fileMatches {
		// Sort matches by StartByte in descending order
		sort.Slice(matches, func(i, j int) bool {
			return matches[i].StartByte > matches[j].StartByte
		})

		// Read file
		content, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", filePath, err)
			continue
		}

		// Apply replacements
		newContent := replaceInFile(content, matches)

		// Write file
		if err := os.WriteFile(filePath, newContent, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", filePath, err)
			continue
		}

		fmt.Printf("Updated: %s (%d replacements)\n", filePath, len(matches))
	}

	// Write locale file
	if err := writeLocaleFile(*output, translations); err != nil {
		fmt.Fprintf(os.Stderr, "error writing locale file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nLocale file written: %s (%d keys)\n", *output, len(translations))
	fmt.Println("Done!")
}

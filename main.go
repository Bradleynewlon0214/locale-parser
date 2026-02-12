package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/alexaandru/go-sitter-forest/vue"
	sitter "github.com/alexaandru/go-tree-sitter-bare"
	"github.com/gosimple/slug"
)

var parser *sitter.Parser
var lang *sitter.Language

func init() {
	parser = sitter.NewParser()
	lang = sitter.NewLanguage(vue.GetLanguage())
	parser.SetLanguage(lang)
}

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

func (m *Match) PrintDiff() {
	fmt.Printf("%s:%d\n", m.File, m.Line)
	fmt.Printf("\t- %s\n", m.Text)
	fmt.Printf("\t+ {{ $t('%s') }}\n\n", m.Key)
}

// func worker(id int, jobs <-chan string, results chan<- []Match) {
// 	for j := range jobs {
// 		m, _ := parse(j)
// 		results <- m
// 	}
// }

func isWhiteSpaceOnly(s string) bool {
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func isSpecialOnly(s string) bool {
	for _, r := range s {
		if !unicode.IsSymbol(r) {
			return false
		}
	}
	return true
}

func generateKey(path string, basepath string, text string, maxslug int, keycounter map[string]int) string {
	relpath, err := filepath.Rel(basepath, path)
	if err != nil {
		relpath = path
	}

	relpath = strings.TrimSpace(path)
	relpath = strings.TrimSuffix(relpath, ".vue")
	relpath = strings.ToLower(relpath)
	relpath = strings.ReplaceAll(relpath, string(filepath.Separator), ".")

	slug.MaxLength = maxslug
	sluged := slug.Make(text)

	key := fmt.Sprintf("%s.%s", relpath, sluged)

	keycounter[key]++
	if keycounter[key] > 1 {
		key = fmt.Sprintf("%s-%d", key, keycounter[key])
	}
	return key
}

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

func writeLocaleFile(path string, translations map[string]string) error {
	existing := make(map[string]string)

	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("failed to parse existing locale file: %w", err)
		}
	}

	// Merge new translations
	for key, value := range translations {
		if _, exists := existing[key]; !exists {
			existing[key] = value
		}
	}

	keys := make([]string, 0, len(existing))
	for k := range existing {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	//unnessecary?
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

func parse(path string, basepath string, maxslug int, keycounter map[string]int) ([]Match, error) {
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

			if isWhiteSpaceOnly(text) {
				continue
			}

			//also check is special chars only

			line := int(node.StartPoint().Row) + 1
			startbyte := node.StartByte()
			endbyte := node.EndByte()

			key := generateKey(path, basepath, text, maxslug, keycounter)

			results = append(results, Match{
				File:      path,
				Line:      line,
				Text:      text,
				StartByte: startbyte,
				EndByte:   endbyte,
				Key:       key,
			})
		}
	}
	return results, nil
}

func walk(path string, basepath string, maxslug int, keycounter map[string]int, results *[]Match) {
	entries, err := os.ReadDir(path)
	if err != nil {
		fmt.Printf("Unable to access %s directory\n", path)
		return
	}

	for _, entry := range entries {
		fullPath := filepath.Join(path, entry.Name())
		if entry.IsDir() {
			walk(fullPath, basepath, maxslug, keycounter, results)
		} else {
			if strings.HasSuffix(entry.Name(), ".vue") {
				matches, _ := parse(fullPath, basepath, maxslug, keycounter)
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
	config := flag.String("config", "", "path to configuration file")
	replace := flag.Bool("replace", false, "enable replacement mode (dry-run by default)")
	write := flag.Bool("write", false, "apply changes to files (use with -replace)")
	output := flag.String("output", "en.json", "path for generated locale JSON file")
	maxslug := flag.Int("max-slug", 30, "maximum slug length in characters")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "\t%s -path ./src                     # Find un-localized text\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\t%s -path ./src -replace            # Dry-run replacement preview\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\t%s -path ./src -replace -write     # Apply replacements\n", os.Args[0])
	}

	flag.Parse()

	if *path == "" {
		fmt.Fprintln(os.Stderr, "error: -path is required")
		flag.Usage()
		os.Exit(1)
	}

	isDir(*path)

	var results []Match
	keycounter := make(map[string]int)

	// jobs := make(chan string, 5)
	// r := make(chan []Match, 5)

	walk(*path, *path, *maxslug, keycounter, &results)

	if len(results) == 0 {
		fmt.Println("No un-localized text found.")
		os.Exit(0)
	}

	if !*replace {
		fmt.Println("Encountered un-localized text!")
		for _, m := range results {
			m.Print()
		}
		os.Exit(1)
	}

	if !*write {
		fmt.Println("=== DRY RUN ===")
		for _, m := range results {
			m.PrintDiff()
		}

		fileSet := make(map[string]bool)
		for _, m := range results {
			fileSet[m.File] = true
		}

		fmt.Println("---")
		fmt.Printf("Summary:\n")
		fmt.Printf("\t%d replacements across %d files\n", len(results), len(fileSet))
		fmt.Printf("\t%d new keys for %s\n", len(results), *output)
		fmt.Printf("\nRun with -write to apply changes.\n")
		os.Exit(0)
	}

	fileMatches := make(map[string][]Match)
	for _, m := range results {
		fileMatches[m.File] = append(fileMatches[m.File], m)
	}

	translations := make(map[string]string)
	for _, m := range results {
		translations[m.Key] = m.Text
	}

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

	if err := writeLocaleFile(*output, translations); err != nil {
		fmt.Fprintf(os.Stderr, "error writing locale file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nLocale file written: %s (%d keys)\n", *output, len(translations))
	fmt.Println("Done!")

	_ = config
}

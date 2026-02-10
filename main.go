package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

type Match struct {
	Line int
	File string
	Text string
}

func (m *Match) Print() {
	fmt.Printf("%s:%d  %q\n", m.File, m.Line, m.Text)
}

func worker(id int, jobs <-chan string, results chan<- []Match) {
	for j := range jobs {
		m, _ := parse(j)
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

func parse(path string) ([]Match, error) {
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
			line := int(node.StartPoint().Row) + 1
			results = append(results, Match{File: path, Line: line, Text: text})
		}
	}
	return results, nil
}

func walk(path string, results *[]Match) {
	entries, err := os.ReadDir(path)
	if err != nil {
		fmt.Printf("Unable to access %s directory\n", path)
		return
	}

	for _, entry := range entries {
		fullPath := filepath.Join(path, entry.Name())
		if entry.IsDir() {
			walk(fullPath, results)
		} else {
			if strings.HasSuffix(entry.Name(), ".vue") {
				matches, _ := parse(fullPath)
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

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()

	if *path == "" {
		fmt.Fprintln(os.Stderr, "error: -path is required")
		flag.Usage()
		os.Exit(1)
	}

	isDir(*path)

	var results []Match

	jobs := make(chan string, 5)
	r := make(chan []Match, 5)

	walk(*path, &results)

	if len(results) >= 0 {

		fmt.Println("Encountered un-localized text!")

		for _, m := range results {
			m.Print()
		}
		os.Exit(1)
	}

	_ = config
}

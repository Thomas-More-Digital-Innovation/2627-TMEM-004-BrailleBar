package main

import "strings"

func Layout(text string, cellsPerLine, linesPerPage int) [][]string {
	var lines []string
	for _, paragraph := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		lines = append(lines, wrap(clean(paragraph), cellsPerLine)...)
	}
	pages := [][]string{} // never nil: the contract rejects null
	for i := 0; i < len(lines); i += linesPerPage {
		pages = append(pages, lines[i:min(i+linesPerPage, len(lines))])
	}
	return pages
}

func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == ' ' {
			return r
		}
		return ' '
	}, s)
}

func wrap(s string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		for len(word) > width {
			if line != "" {
				lines = append(lines, line)
				line = ""
			}
			lines = append(lines, word[:width])
			word = word[width:]
		}
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) <= width:
			line += " " + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != "" || len(lines) == 0 {
		lines = append(lines, line)
	}
	return lines
}

package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestLayout(t *testing.T) {
	forty := strings.Repeat("a", 40)
	cases := []struct {
		name string
		text string
		w    int
		want [][]string
	}{
		{"fits on one line", "hello world", 30, [][]string{{"hello world"}}},
		{"wraps at the space", "hello world", 10, [][]string{{"hello", "world"}}},
		{"long word is cut", forty, 30, [][]string{{forty[:30], forty[30:]}}},
		{"long word after a short one", "hi " + forty, 30, [][]string{{"hi", forty[:30], forty[30:]}}},
		{"blank line kept", "a\n\nb", 30, [][]string{{"a", "", "b"}}},
		{"windows line endings", "a\r\nb", 30, [][]string{{"a", "b"}}},
		{"punctuation becomes space", "Hello, world!", 30, [][]string{{"Hello world"}}},
		{"accents become space", "café", 30, [][]string{{"caf"}}},
		{"empty text is one blank line", "", 30, [][]string{{""}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Layout(c.text, c.w, 17); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestLayoutPages(t *testing.T) {
	text := strings.TrimSuffix(strings.Repeat("x\n", 40), "\n") // 40 lines
	pages := Layout(text, 30, 17)
	if len(pages) != 3 || len(pages[0]) != 17 || len(pages[1]) != 17 || len(pages[2]) != 6 {
		t.Fatalf("want pages of 17, 17 and 6 lines, got %d pages", len(pages))
	}
}

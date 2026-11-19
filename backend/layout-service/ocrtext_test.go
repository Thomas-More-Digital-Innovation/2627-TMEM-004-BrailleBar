package main

import (
	"strings"
	"testing"

	"BrailleBar/platform"
)

func page(lines ...string) platform.OcrPage {
	p := platform.OcrPage{Lines: []platform.OcrLine{}}
	for _, l := range lines {
		p.Lines = append(p.Lines, platform.OcrLine{Text: l, Words: []platform.OcrWord{}})
	}
	return p
}

func TestOcrTextRebuildsParagraphs(t *testing.T) {
	cases := []struct {
		name  string
		pages []platform.OcrPage
		want  []string
	}{
		{"printed lines of one paragraph are joined",
			[]platform.OcrPage{page("Anomaliile dento-maxilare se numără printre cele mai", "frecvente afecțiuni ale sistemului stomatognat.")},
			[]string{"Anomaliile dento-maxilare se numără printre cele mai frecvente afecțiuni ale sistemului stomatognat."}},
		{"a word split with a hyphen is joined back",
			[]platform.OcrPage{page("recomanda expansiunea ca tratament primar al obstruc-", "ției nazale.")},
			[]string{"recomanda expansiunea ca tratament primar al obstrucției nazale."}},
		{"a hyphen before a capital stays a hyphen",
			[]platform.OcrPage{page("the meeting with the team of Smith-", "Jones about the long report.")},
			[]string{"the meeting with the team of Smith- Jones about the long report."}},
		{"a heading and a short last line end their paragraph",
			[]platform.OcrPage{page("INTRODUCERE", "the first line of a long paragraph goes here", "and it ends short", "next paragraph")},
			[]string{"INTRODUCERE", "the first line of a long paragraph goes here and it ends short", "next paragraph"}},
		{"an empty line ends a paragraph",
			[]platform.OcrPage{page("one paragraph without a stop", "", "another")},
			[]string{"one paragraph without a stop", "another"}},
		{"a paragraph continues on the next page",
			[]platform.OcrPage{page("this sentence starts on page one and", "goes on"), page("and ends on page two here.")},
			[]string{"this sentence starts on page one and goes on", "and ends on page two here."}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.Split(ocrText(c.pages), "\n"); strings.Join(got, "|") != strings.Join(c.want, "|") {
				t.Fatalf("got\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

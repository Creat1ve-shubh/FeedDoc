package rules

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	shoutPunct   = regexp.MustCompile(`[!?*]+`)
	emptySep     = regexp.MustCompile(`\s*[-|,:/]\s*(?:[-|,:/]\s*)+`)
	edgeSep      = regexp.MustCompile(`^[\s\-|,:/.]+|[\s\-|,:/]+$`)
	smallConnect = map[string]bool{"A": true, "AN": true, "AND": true, "THE": true, "FOR": true, "OF": true, "IN": true, "ON": true, "TO": true, "OR": true, "BY": true, "AT": true}
)

// CleanTitle returns the title with the problems title.promotional and
// title.too_long flag fixed: promotional phrases and shouting punctuation
// removed, an all-caps title put in title case, and the result cut to
// Google's 150 characters at a word boundary. A title with none of these
// problems comes back unchanged.
func CleanTitle(title string) string {
	t := strings.TrimSpace(title)
	if promoPattern.MatchString(t) {
		t = promoPattern.ReplaceAllString(t, " ")
		t = shoutPunct.ReplaceAllString(t, " ")
		t = emptySep.ReplaceAllString(t, " - ")
		t = edgeSep.ReplaceAllString(strings.Join(strings.Fields(t), " "), "")
	}
	if shouting(t) {
		t = unshout(t)
	}
	t = strings.Join(strings.Fields(t), " ")
	if r := []rune(t); len(r) > maxTitleLen {
		cut := string(r[:maxTitleLen])
		if i := strings.LastIndexByte(cut, ' '); i > maxTitleLen/2 {
			cut = cut[:i]
		}
		t = edgeSep.ReplaceAllString(cut, "")
	}
	if t == "" {
		return strings.TrimSpace(title) // never blank a title; title.missing is worse
	}
	return t
}

// unshout puts all-caps words of four or more letters in title case and
// lowercases short connectors. Other short words (USB, XL, LED) are kept.
func unshout(t string) string {
	words := strings.Fields(t)
	for i, w := range words {
		letters, upper := 0, 0
		for _, r := range w {
			if unicode.IsLetter(r) {
				letters++
				if unicode.IsUpper(r) {
					upper++
				}
			}
		}
		if letters == 0 || upper != letters {
			continue
		}
		switch {
		case i > 0 && smallConnect[w]:
			words[i] = strings.ToLower(w)
		case letters >= 4:
			r := []rune(strings.ToLower(w))
			for j, c := range r {
				if unicode.IsLetter(c) {
					r[j] = unicode.ToUpper(c)
					break
				}
			}
			words[i] = string(r)
		}
	}
	return strings.Join(words, " ")
}

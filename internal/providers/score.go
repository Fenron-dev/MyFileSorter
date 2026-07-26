package providers

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/dennis/myfilesorter/internal/domain"
)

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func score(query domain.MetadataSearchQuery, candidate domain.MetadataCandidate) float64 {
	if query.ASIN != "" && strings.EqualFold(query.ASIN, candidate.ASIN) {
		return 1
	}
	if query.ISBN != "" && compactID(query.ISBN) == compactID(candidate.ISBN) {
		return .99
	}
	value := .68*similarity(query.Title, candidate.Title) + .24*similarity(query.Author, candidate.Author)
	if query.Narrator != "" && candidate.Narrator != "" {
		value += .08 * similarity(query.Narrator, candidate.Narrator)
	} else {
		value += .04
	}
	if value > 1 {
		return 1
	}
	return value
}

func similarity(left, right string) float64 {
	leftTokens := tokens(left)
	rightTokens := tokens(right)
	if len(leftTokens) == 0 || len(rightTokens) == 0 {
		return 0
	}
	intersection := 0
	union := make(map[string]struct{}, len(leftTokens)+len(rightTokens))
	for token := range leftTokens {
		union[token] = struct{}{}
		if _, found := rightTokens[token]; found {
			intersection++
		}
	}
	for token := range rightTokens {
		union[token] = struct{}{}
	}
	return float64(intersection) / float64(len(union))
}

func tokens(value string) map[string]struct{} {
	value = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, value)
	result := make(map[string]struct{})
	for _, token := range strings.Fields(nonWord.ReplaceAllString(value, " ")) {
		result[token] = struct{}{}
	}
	return result
}

func compactID(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToUpper(r)
		}
		return -1
	}, value)
}

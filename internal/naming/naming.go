package naming

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dennis/myfilesorter/internal/domain"
)

var (
	invalidChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1F]`)
	spaces       = regexp.MustCompile(`\s+`)
	reserved     = map[string]struct{}{
		"con": {}, "prn": {}, "aux": {}, "nul": {},
		"com1": {}, "com2": {}, "com3": {}, "com4": {}, "com5": {}, "com6": {}, "com7": {}, "com8": {}, "com9": {},
		"lpt1": {}, "lpt2": {}, "lpt3": {}, "lpt4": {}, "lpt5": {}, "lpt6": {}, "lpt7": {}, "lpt8": {}, "lpt9": {},
	}
)

func BookDirectory(meta domain.BookMetadata) (string, error) {
	author := Segment(meta.Author)
	title := Segment(meta.Title)
	if author == "" || title == "" {
		return "", fmt.Errorf("author and title are required")
	}
	if strings.TrimSpace(meta.Series) == "" {
		return filepath.Join(author, title), nil
	}
	if strings.TrimSpace(meta.SeriesSequence) == "" {
		return "", fmt.Errorf("series sequence is required when a series is set")
	}
	series := Segment(meta.Series)
	sequence := Sequence(meta.SeriesSequence)
	if sequence == "" {
		return "", fmt.Errorf("series sequence is invalid")
	}
	return filepath.Join(author, series, sequence+" - "+title), nil
}

func TrackName(index, total int, title, extension string) string {
	width := 2
	if digits := len(strconv.Itoa(total)); digits > width {
		width = digits
	}
	extension = strings.ToLower(extension)
	if extension != "" && !strings.HasPrefix(extension, ".") {
		extension = "." + extension
	}
	return fmt.Sprintf("%0*d - %s%s", width, index, Segment(title), extension)
}

func EbookName(index, total int, title, extension string) string {
	if total <= 1 {
		extension = strings.ToLower(extension)
		if extension != "" && !strings.HasPrefix(extension, ".") {
			extension = "." + extension
		}
		return Segment(title) + extension
	}
	return TrackName(index, total, title, extension)
}

func Segment(value string) string {
	value = strings.ToValidUTF8(value, "")
	value = invalidChars.ReplaceAllString(value, "-")
	value = spaces.ReplaceAllString(strings.TrimSpace(value), " ")
	value = strings.Trim(value, ". ")
	if value == "" {
		return ""
	}
	base := strings.ToLower(strings.TrimSuffix(value, filepath.Ext(value)))
	if _, found := reserved[base]; found {
		value = "_" + value
	}
	const maxRunes = 120
	if utf8.RuneCountInString(value) > maxRunes {
		runes := []rune(value)
		value = strings.TrimSpace(string(runes[:maxRunes]))
	}
	return value
}

func Sequence(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), ",", ".")
	parts := strings.SplitN(value, ".", 2)
	whole, err := strconv.Atoi(parts[0])
	if err != nil || whole < 0 {
		return ""
	}
	result := fmt.Sprintf("%02d", whole)
	if len(parts) == 2 {
		fraction := strings.TrimRight(parts[1], "0")
		if fraction != "" {
			if _, err := strconv.Atoi(fraction); err != nil {
				return ""
			}
			result += "." + fraction
		}
	}
	return result
}

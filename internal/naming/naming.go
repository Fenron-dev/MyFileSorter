package naming

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dennis/myfilesorter/internal/domain"
	"golang.org/x/text/unicode/norm"
)

var (
	invalidChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1F]`)
	spaces       = regexp.MustCompile(`\s+`)
	trackPrefix  = regexp.MustCompile(`^\s*(\d+)(?:\s*[-._]+\s*|\s+)(.*)$`)
	numericName  = regexp.MustCompile(`^\s*(\d+)\s*$`)
	reserved     = map[string]struct{}{
		"con": {}, "prn": {}, "aux": {}, "nul": {},
		"com1": {}, "com2": {}, "com3": {}, "com4": {}, "com5": {}, "com6": {}, "com7": {}, "com8": {}, "com9": {},
		"lpt1": {}, "lpt2": {}, "lpt3": {}, "lpt4": {}, "lpt5": {}, "lpt6": {}, "lpt7": {}, "lpt8": {}, "lpt9": {},
	}
)

func BookDirectory(meta domain.BookMetadata) (string, error) {
	return BookDirectoryWithWidth(meta, 2)
}

func BookDirectoryWithWidth(meta domain.BookMetadata, width int) (string, error) {
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
	sequence := SequenceWithWidth(meta.SeriesSequence, width)
	if sequence == "" {
		return "", fmt.Errorf("series sequence is invalid")
	}
	return filepath.Join(author, series, sequence+" - "+title), nil
}

func TrackName(index, total int, title, extension string) string {
	return TrackNameWithWidth(index, total, title, extension, 0)
}

func TrackNameWithWidth(index, total int, title, extension string, width int) string {
	width = resolvedTrackWidth(width, total, index)
	extension = strings.ToLower(extension)
	if extension != "" && !strings.HasPrefix(extension, ".") {
		extension = "." + extension
	}
	return fmt.Sprintf("%0*d - %s%s", width, index, Segment(title), extension)
}

// SourceTrackName keeps the meaningful part of an existing file name while
// normalising its numeric prefix and dot/underscore separators.
func SourceTrackName(file domain.AudioFile, fallbackIndex, total, width int) string {
	extension := strings.ToLower(file.Extension)
	if extension == "" {
		extension = strings.ToLower(filepath.Ext(file.Name))
	}
	base := strings.TrimSuffix(file.Name, filepath.Ext(file.Name))
	track := file.Track
	match := trackPrefix.FindStringSubmatch(base)
	if len(match) == 0 {
		if numeric := numericName.FindStringSubmatch(base); len(numeric) == 2 {
			match = []string{numeric[0], numeric[1], ""}
		}
	}
	if len(match) == 3 {
		if parsed, err := strconv.Atoi(match[1]); err == nil && plausibleTrackPrefix(parsed, track, fallbackIndex, total) {
			if track <= 0 {
				track = parsed
			}
			base = match[2]
		}
	}
	if track <= 0 {
		track = fallbackIndex
	}
	base = strings.NewReplacer("_", " ", ".", " ").Replace(base)
	base = spaces.ReplaceAllString(strings.TrimSpace(base), " ")
	base = strings.Trim(base, "- ")
	if base == "" {
		base = "Track"
	}
	return TrackNameWithWidth(track, total, base, extension, width)
}

func plausibleTrackPrefix(parsed, track, fallbackIndex, total int) bool {
	return parsed > 0 && (parsed == track || parsed == fallbackIndex || (total > 1 && parsed <= total))
}

func resolvedTrackWidth(width, total, index int) int {
	if width == 0 {
		width = 2
	} else if width < 0 {
		width = len(strconv.Itoa(max(total, index)))
	}
	if width < 1 {
		return 1
	}
	if width > 6 {
		width = 6
	}
	if digits := len(strconv.Itoa(index)); digits > width {
		return digits
	}
	return width
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
	value = norm.NFC.String(value)
	value = invalidChars.ReplaceAllString(value, "-")
	value = spaces.ReplaceAllString(strings.TrimSpace(value), " ")
	value = strings.Trim(value, ". ")
	if value == "" {
		return ""
	}
	// Windows reserves a device name even when it has one or more suffixes
	// (for example CON.txt or CON.backup.txt). Only the segment before the
	// first dot is relevant for this check.
	base := strings.ToLower(value)
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	if _, found := reserved[base]; found {
		value = "_" + value
	}
	const (
		maxRunes = 120
		maxBytes = 180
	)
	if utf8.RuneCountInString(value) > maxRunes || len(value) > maxBytes {
		digest := sha256.Sum256([]byte(value))
		suffix := "~" + hex.EncodeToString(digest[:4])
		limitBytes := maxBytes - len(suffix)
		limitRunes := maxRunes - utf8.RuneCountInString(suffix)
		var builder strings.Builder
		count := 0
		for _, character := range value {
			encoded := string(character)
			if count >= limitRunes || builder.Len()+len(encoded) > limitBytes {
				break
			}
			builder.WriteString(encoded)
			count++
		}
		value = strings.Trim(strings.TrimSpace(builder.String()), ". ") + suffix
	}
	return value
}

func Sequence(value string) string {
	return SequenceWithWidth(value, 2)
}

func SequenceWithWidth(value string, width int) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), ",", ".")
	parts := strings.SplitN(value, ".", 2)
	whole, err := strconv.Atoi(parts[0])
	if err != nil || whole < 0 {
		return ""
	}
	if width < 1 {
		width = 1
	}
	if width > 6 {
		return ""
	}
	result := fmt.Sprintf("%0*d", width, whole)
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

package scanner

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dennis/myfilesorter/internal/domain"
)

var numberChunk = regexp.MustCompile(`\d+|\D+`)

var (
	folderSeparators = regexp.MustCompile(`[._]+`)
	releaseMarker    = regexp.MustCompile(`(?i)\b(?:abook|audiobook)\b`)
	editionMarker    = regexp.MustCompile(`(?i)\b(ungekürzt|ungekuerzt|gekürzt|gekuerzt|unabridged|abridged)\b`)
	emptyBrackets    = regexp.MustCompile(`(?:\(\s*\)|\[\s*\])`)
	trailingSequence = regexp.MustCompile(`(?i)^(.*?)\s+(?:band|teil)?\s*(\d+(?:[.,]\d+)?)$`)
)

type folderMetadata struct {
	Title          string
	Author         string
	Series         string
	SeriesSequence string
	EditionInfo    string
}

func proposalID(paths []string) string {
	hash := sha256.Sum256([]byte(strings.Join(paths, "\x00")))
	return hex.EncodeToString(hash[:8])
}

func inferTitle(root, groupPath string, files []domain.AudioFile, folder folderMetadata) (string, domain.Evidence) {
	if value := commonMetadata(files, func(m domain.EmbeddedMetadata) string { return m.Album }); value != "" {
		return value, domain.Evidence{Value: value, Source: "album_tag", Confidence: .9}
	}
	if len(files) == 1 && files[0].Metadata.Title != "" {
		value := files[0].Metadata.Title
		return value, domain.Evidence{Value: value, Source: "title_tag", Confidence: .82}
	}
	if folder.Title != "" {
		return folder.Title, domain.Evidence{Value: folder.Title, Source: "folder", Confidence: .68}
	}
	if len(files) > 0 {
		value := cleanName(strings.TrimSuffix(files[0].Name, filepath.Ext(files[0].Name)))
		return value, domain.Evidence{Value: value, Source: "filename", Confidence: .52}
	}
	return "", domain.Evidence{}
}

func inferAuthor(files []domain.AudioFile, folder folderMetadata) (string, domain.Evidence) {
	if value := commonMetadata(files, func(m domain.EmbeddedMetadata) string { return m.AlbumArtist }); value != "" {
		return value, domain.Evidence{Value: value, Source: "album_artist_tag", Confidence: .92}
	}
	if value := commonMetadata(files, func(m domain.EmbeddedMetadata) string { return m.Artist }); value != "" {
		return value, domain.Evidence{Value: value, Source: "artist_tag", Confidence: .87}
	}
	if folder.Author != "" {
		return folder.Author, domain.Evidence{Value: folder.Author, Source: "folder", Confidence: .64}
	}
	return "", domain.Evidence{}
}

func inferFolderMetadata(root, groupPath string, files []domain.AudioFile) folderMetadata {
	if len(files) == 1 && samePath(groupPath, files[0].Path) {
		return folderMetadata{}
	}
	if samePath(groupPath, root) && !hasDiscHierarchy(groupPath, files) {
		return folderMetadata{}
	}
	rawName := filepath.Base(groupPath)
	name := cleanFolderName(rawName)
	if name == "" {
		return folderMetadata{}
	}
	parts := strings.Split(name, " - ")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	result := folderMetadata{Title: name, EditionInfo: extractEditionInfo(rawName)}
	if len(parts) >= 2 {
		result.Author = parts[0]
		result.Title = strings.Join(parts[1:], " - ")
		if match := trailingSequence.FindStringSubmatch(result.Title); len(match) == 3 {
			result.Title = strings.TrimSpace(match[1])
			result.Series = result.Title
			result.SeriesSequence = strings.ReplaceAll(match[2], ",", ".")
		}
	}
	if len(parts) >= 3 {
		if match := trailingSequence.FindStringSubmatch(parts[1]); len(match) == 3 {
			result.Series = strings.TrimSpace(match[1])
			result.SeriesSequence = strings.ReplaceAll(match[2], ",", ".")
			result.Title = strings.Join(parts[2:], " - ")
		}
	}
	return result
}

func hasDiscHierarchy(groupPath string, files []domain.AudioFile) bool {
	for _, file := range files {
		if discNumberForPath(groupPath, file.Path) > 0 {
			return true
		}
	}
	return false
}

func extractEditionInfo(value string) string {
	value = normalizeEditionUnicode(value)
	match := editionMarker.FindStringSubmatch(value)
	if len(match) != 2 {
		return ""
	}
	value = strings.ToLower(match[1])
	switch value {
	case "ungekürzt", "ungekuerzt", "unabridged":
		return "Ungekürzt"
	case "gekürzt", "gekuerzt", "abridged":
		return "Gekürzt"
	default:
		return ""
	}
}

func normalizeEditionUnicode(value string) string {
	replacer := strings.NewReplacer(
		"u\u0308", "ü", "U\u0308", "Ü",
		"a\u0308", "ä", "A\u0308", "Ä",
		"o\u0308", "ö", "O\u0308", "Ö",
	)
	return replacer.Replace(value)
}

func cleanFolderName(value string) string {
	value = normalizeEditionUnicode(value)
	lower := strings.ToLower(value)
	for extension := range audioExtensions {
		if strings.HasSuffix(lower, extension) {
			value = strings.TrimSpace(value[:len(value)-len(extension)])
			break
		}
	}
	value = folderSeparators.ReplaceAllString(value, " ")
	value = editionMarker.ReplaceAllString(value, " ")
	value = releaseMarker.ReplaceAllString(value, " ")
	value = emptyBrackets.ReplaceAllString(value, " ")
	value = strings.Join(strings.Fields(value), " ")
	value = strings.Trim(value, " ,-_()[]")
	return value
}

func inferField(files []domain.AudioFile, field string) (string, domain.Evidence) {
	value := commonMetadata(files, func(m domain.EmbeddedMetadata) string {
		switch field {
		case "series":
			return m.Series
		case "seriesSequence":
			return m.SeriesSequence
		case "narrator":
			return m.Narrator
		case "language":
			return m.Language
		case "asin":
			return m.ASIN
		case "isbn":
			return m.ISBN
		default:
			return ""
		}
	})
	if value == "" {
		return "", domain.Evidence{}
	}
	confidence := .88
	if field == "asin" || field == "isbn" {
		confidence = .99
	}
	return value, domain.Evidence{Value: value, Source: field + "_tag", Confidence: confidence}
}

func commonMetadata(files []domain.AudioFile, pick func(domain.EmbeddedMetadata) string) string {
	var candidate string
	for _, file := range files {
		value := strings.TrimSpace(pick(file.Metadata))
		if value == "" {
			continue
		}
		if candidate == "" {
			candidate = value
			continue
		}
		if !strings.EqualFold(candidate, value) {
			return ""
		}
	}
	return candidate
}

func averageRequiredConfidence(meta domain.BookMetadata) float64 {
	title := meta.Evidence["title"].Confidence
	author := meta.Evidence["author"].Confidence
	return (title + author) / 2
}

func cleanName(value string) string {
	value = trackPrefix.ReplaceAllString(value, "")
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.TrimSpace(value)
	return strings.Join(strings.Fields(value), " ")
}

func hasTrackEvidence(files []domain.AudioFile) bool {
	for _, file := range files {
		if file.Track <= 0 {
			return false
		}
	}
	return true
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func zeroLast(left, right int) bool {
	if left == 0 {
		return false
	}
	if right == 0 {
		return true
	}
	return left < right
}

func naturalLess(left, right string) bool {
	lparts := numberChunk.FindAllString(strings.ToLower(left), -1)
	rparts := numberChunk.FindAllString(strings.ToLower(right), -1)
	for i := 0; i < len(lparts) && i < len(rparts); i++ {
		if lparts[i] == rparts[i] {
			continue
		}
		ln, lerr := strconv.Atoi(lparts[i])
		rn, rerr := strconv.Atoi(rparts[i])
		if lerr == nil && rerr == nil {
			return ln < rn
		}
		return lparts[i] < rparts[i]
	}
	return len(lparts) < len(rparts)
}

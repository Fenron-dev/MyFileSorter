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

func proposalID(paths []string) string {
	hash := sha256.Sum256([]byte(strings.Join(paths, "\x00")))
	return hex.EncodeToString(hash[:8])
}

func inferTitle(root, groupPath string, files []domain.AudioFile) (string, domain.Evidence) {
	if value := commonMetadata(files, func(m domain.EmbeddedMetadata) string { return m.Album }); value != "" {
		return value, domain.Evidence{Value: value, Source: "album_tag", Confidence: .9}
	}
	if len(files) == 1 && files[0].Metadata.Title != "" {
		value := files[0].Metadata.Title
		return value, domain.Evidence{Value: value, Source: "title_tag", Confidence: .82}
	}
	if !samePath(groupPath, root) && filepath.Ext(groupPath) == "" {
		value := cleanName(filepath.Base(groupPath))
		if match := seriesPrefix.FindStringSubmatch(value); len(match) == 3 {
			value = match[2]
		}
		return value, domain.Evidence{Value: value, Source: "folder", Confidence: .58}
	}
	if len(files) > 0 {
		value := cleanName(strings.TrimSuffix(files[0].Name, filepath.Ext(files[0].Name)))
		return value, domain.Evidence{Value: value, Source: "filename", Confidence: .52}
	}
	return "", domain.Evidence{}
}

func inferAuthor(files []domain.AudioFile) (string, domain.Evidence) {
	if value := commonMetadata(files, func(m domain.EmbeddedMetadata) string { return m.AlbumArtist }); value != "" {
		return value, domain.Evidence{Value: value, Source: "album_artist_tag", Confidence: .92}
	}
	if value := commonMetadata(files, func(m domain.EmbeddedMetadata) string { return m.Artist }); value != "" {
		return value, domain.Evidence{Value: value, Source: "artist_tag", Confidence: .87}
	}
	return "", domain.Evidence{}
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

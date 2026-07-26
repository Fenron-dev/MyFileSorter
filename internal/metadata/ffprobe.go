package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/dennis/myfilesorter/internal/domain"
)

type FFProbeReader struct {
	path string
}

func NewFFProbeReader() Reader {
	path, err := exec.LookPath("ffprobe")
	if err != nil {
		return NoopReader{}
	}
	return FFProbeReader{path: path}
}

func (r FFProbeReader) Available() bool { return r.path != "" }

func (r FFProbeReader) Read(ctx context.Context, path string) (domain.EmbeddedMetadata, error) {
	cmd := exec.CommandContext(ctx, r.path, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	out, err := cmd.Output()
	if err != nil {
		return domain.EmbeddedMetadata{}, fmt.Errorf("ffprobe: %w", err)
	}
	return parseFFProbe(out)
}

type ffprobeDocument struct {
	Format struct {
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
	Streams []struct {
		CodecType string            `json:"codec_type"`
		Duration  string            `json:"duration"`
		Tags      map[string]string `json:"tags"`
	} `json:"streams"`
}

func parseFFProbe(data []byte) (domain.EmbeddedMetadata, error) {
	var doc ffprobeDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return domain.EmbeddedMetadata{}, fmt.Errorf("parse ffprobe output: %w", err)
	}

	tags := normalizeTags(doc.Format.Tags)
	duration := doc.Format.Duration
	for _, stream := range doc.Streams {
		if stream.CodecType != "audio" {
			continue
		}
		for key, value := range normalizeTags(stream.Tags) {
			if _, exists := tags[key]; !exists {
				tags[key] = value
			}
		}
		if duration == "" {
			duration = stream.Duration
		}
		break
	}

	return domain.EmbeddedMetadata{
		Title:          firstTag(tags, "title"),
		Album:          firstTag(tags, "album"),
		Artist:         firstTag(tags, "artist", "author"),
		AlbumArtist:    firstTag(tags, "album_artist", "albumartist"),
		Series:         firstTag(tags, "series", "mvnm"),
		SeriesSequence: firstTag(tags, "series-part", "series_part", "seriespart", "mvin"),
		Narrator:       firstTag(tags, "narrator", "composer"),
		Language:       firstTag(tags, "language", "lang"),
		ASIN:           firstTag(tags, "asin", "audible_asin", "audible asin"),
		ISBN:           firstTag(tags, "isbn"),
		Track:          leadingInt(firstTag(tags, "track", "tracknumber")),
		Disc:           leadingInt(firstTag(tags, "disc", "discnumber", "disk")),
		DurationMillis: durationMillis(duration),
	}, nil
}

func normalizeTags(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		out[key] = value
		switch {
		case strings.HasSuffix(key, ":asin"), strings.HasSuffix(key, ".asin"):
			out["asin"] = value
		case strings.HasSuffix(key, ":isbn"), strings.HasSuffix(key, ".isbn"):
			out["isbn"] = value
		case strings.HasSuffix(key, ":series-part"), strings.HasSuffix(key, ":series_part"):
			out["series-part"] = value
		case strings.HasSuffix(key, ":series"):
			out["series"] = value
		}
	}
	return out
}

func firstTag(tags map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(tags[key]); value != "" {
			return value
		}
	}
	return ""
}

func leadingInt(value string) int {
	value = strings.TrimSpace(value)
	if slash := strings.IndexByte(value, '/'); slash >= 0 {
		value = value[:slash]
	}
	n, _ := strconv.Atoi(value)
	return n
}

func durationMillis(value string) int64 {
	seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	return int64(seconds * 1000)
}
